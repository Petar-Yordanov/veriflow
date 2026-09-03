package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"veriflow/internal/engine"
)

const (
	ExitOK               = 0
	ExitValidationFailed = 2
	ExitRuntimeFailed    = 3
	ExitUsageError       = 4
	ExitInternalError    = 5
)

func Run(args []string) int { return RunContext(context.Background(), args) }

func RunContext(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" {
		printRootHelp()
		return ExitOK
	}
	switch args[0] {
	case "version", "--version":
		if len(args) > 1 && args[1] == "--json" {
			if err := printJSON(map[string]any{"version": engine.EngineVersion, "commit": engine.BuildCommit, "buildDate": engine.BuildDate}); err != nil {
				return fail(fmt.Errorf("serialize version output: %w", err))
			}
		} else {
			fmt.Println(engine.EngineVersion)
		}
		return ExitOK
	case "schema":
		return runSchema(args[1:])
	case "discover":
		return runDiscover(args[1:])
	case "validate":
		return runValidate(args[1:])
	case "run":
		return runRun(ctx, args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n\n", args[0])
		printRootHelp()
		return ExitUsageError
	}
}

func printRootHelp() {
	fmt.Print(`Veriflow - YAML API test engine

Usage:
  veriflow schema [spec|config] [--output FILE]
  veriflow discover suites PROJECT_ROOT [--json]
  veriflow discover requests PROJECT_ROOT [--json]
  veriflow discover environments PROJECT_ROOT [--json]
  veriflow discover tests PROJECT_ROOT [--json]
  veriflow validate file SUITE [--project-root ROOT] [-e NAME] [--json]
  veriflow validate project PROJECT_ROOT [--json]
  veriflow run suite SUITE [options]
  veriflow run discovered PROJECT_ROOT [options]
  veriflow version [--json]

Pipeline/report options:
  --report-junit FILE               Write JUnit XML.
  --report-consolidated-json FILE   Write one aggregate JSON report for discovered runs.
  --var-from-env NAME[=ENV_NAME]    Import an OS environment variable.
  --secret-var-from-env NAME[=ENV]  Import and redact an OS environment variable.
  --max-response-bytes N            Cap response bodies (default 10 MiB).
  --run-timeout-ms N                Cancel the whole run after N milliseconds.
  --suite-timeout-ms N              Default maximum duration for each suite.
  --test-timeout-ms N               Default maximum duration for each test.
  --cleanup-timeout-ms N            Bounded teardown window after cancellation.
  --artifacts-root DIR              Override the project artifacts directory.
  Project defaults may be placed in PROJECT_ROOT/veriflow.yml; CLI flags override them.
  --exclude-test-id ID              Exclude tests with this id.
  --exclude-test-name NAME          Exclude tests with this name.
  --exclude-tag TAG                 Exclude tests containing this tag.
  --exclude-suite-id ID             Exclude discovered suites with this id.
  --exclude-suite-name NAME         Exclude discovered suites with this name.
  --fail-fast                       Stop a discovered run after the first failed suite.
  --shard INDEX/TOTAL               Deterministically select discovered suites for one CI shard.
  --fail-if-no-tests                Fail if selection executes zero tests.
  --no-fail-if-no-tests             Override project config and allow zero selected tests.
  --ci                              Enable pipeline-safe selection checks.
  --ca-file FILE                     Add a PEM CA bundle for HTTPS.
  --client-cert FILE                 Client certificate for mTLS (PEM).
  --client-key FILE                  Client private key for mTLS (PEM).
  --proxy URL                        Override HTTP/HTTPS proxy.
  --cookie-jar                       Persist response cookies across requests in the run.
  --no-cookie-jar                    Override project config and disable the cookie jar.
  --insecure-skip-tls-verify         Disable TLS certificate verification (explicit opt-in).
  --verify-tls                        Override project config and require TLS verification.

Environment convention:
  -e/--environment accepts an environment name such as "dev". Veriflow resolves
  it from PROJECT_ROOT/environments/dev.yml. Environment paths are intentionally
  rejected.
`)
}

func runSchema(args []string) int {
	target := "spec"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		target = args[0]
		args = args[1:]
	}
	opts, err := parseOptions(args, map[string]bool{"--output": true})
	if err != nil {
		return usageErr(err)
	}
	var b []byte
	switch target {
	case "spec":
		b, err = engine.SpecJSONSchemaBytes()
	case "config", "project":
		b, err = engine.ProjectConfigJSONSchemaBytes()
	default:
		return usageErr(fmt.Errorf("unknown schema target %q (expected spec or config)", target))
	}
	if err != nil {
		return fail(err)
	}
	if path := opts.one("--output"); path != "" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
			return fail(err)
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
			return fail(err)
		}
		return ExitOK
	}
	fmt.Println(string(b))
	return ExitOK
}

func runDiscover(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: veriflow discover {suites|requests|environments|tests} PROJECT_ROOT [--json]")
		return ExitUsageError
	}
	kind, root := args[0], args[1]
	opts, err := parseOptions(args[2:], map[string]bool{"--json": false})
	if err != nil {
		return usageErr(err)
	}
	jsonOut := opts.flags["--json"]
	d, err := engine.New().Discover(root)
	if err != nil {
		return renderValidationError(err, root, jsonOut)
	}
	var items []string
	title := ""
	switch kind {
	case "suites":
		items = d.Suites
		title = "Suites"
	case "requests":
		items = d.Requests
		title = "Request Definitions"
	case "environments":
		items = d.Environments
		title = "Environments"
	case "tests":
		return renderDiscoveredTests(d, jsonOut)
	default:
		fmt.Fprintln(os.Stderr, "unknown discover target:", kind)
		return ExitUsageError
	}
	display := []string{}
	for _, p := range items {
		if rel, err := filepath.Rel(d.ProjectRoot, p); err == nil {
			display = append(display, rel)
		} else {
			display = append(display, p)
		}
	}
	if jsonOut {
		if err := printJSON(map[string]any{"projectRoot": d.ProjectRoot, "title": title, "paths": display}); err != nil {
			return fail(fmt.Errorf("serialize discovery output: %w", err))
		}
		return ExitOK
	}
	fmt.Println(title)
	for _, p := range display {
		fmt.Println("  " + p)
	}
	return ExitOK
}

type discoveredTest struct {
	SuitePath string   `json:"suitePath"`
	SuiteID   string   `json:"suiteId,omitempty"`
	SuiteName string   `json:"suiteName,omitempty"`
	TestID    string   `json:"testId"`
	TestName  string   `json:"testName,omitempty"`
	Tags      []string `json:"tags,omitempty"`
	Skipped   bool     `json:"skipped,omitempty"`
}

func renderDiscoveredTests(d engine.DiscoveryResult, jsonOut bool) int {
	e := engine.New()
	rows := []discoveredTest{}
	for _, suitePath := range d.Suites {
		doc, err := e.Load(suitePath)
		if err != nil {
			return renderValidationError(err, suitePath, jsonOut)
		}
		suite, ok := doc.Typed.(engine.TestSuiteSpec)
		if !ok {
			continue
		}
		rel, err := filepath.Rel(d.ProjectRoot, suitePath)
		if err != nil {
			rel = suitePath
		}
		for _, baseTest := range suite.Tests {
			for _, test := range engine.ExpandTestCases(baseTest) {
				rows = append(rows, discoveredTest{
					SuitePath: filepath.ToSlash(rel), SuiteID: suite.Metadata.ID, SuiteName: suite.Info.Name,
					TestID: test.ID, TestName: test.Name, Tags: append([]string{}, test.Tags...), Skipped: test.Skip,
				})
			}
		}
	}
	if jsonOut {
		if err := printJSON(map[string]any{"projectRoot": d.ProjectRoot, "tests": rows}); err != nil {
			return fail(fmt.Errorf("serialize discovered tests: %w", err))
		}
		return ExitOK
	}
	fmt.Println("Tests")
	for _, row := range rows {
		labels := ""
		if len(row.Tags) > 0 {
			labels = " [" + strings.Join(row.Tags, ",") + "]"
		}
		name := row.TestName
		if name != "" {
			name = " - " + name
		}
		skip := ""
		if row.Skipped {
			skip = " (skipped)"
		}
		fmt.Printf("  %s :: %s%s%s%s\n", row.SuitePath, row.TestID, name, labels, skip)
	}
	return ExitOK
}

func runValidate(args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: veriflow validate {file|project} PATH [options]")
		return ExitUsageError
	}
	mode, target := args[0], args[1]
	opts, err := parseOptions(args[2:], map[string]bool{"--json": false, "--project-root": true, "--environment": true, "-e": true})
	if err != nil {
		return usageErr(err)
	}
	jsonOut := opts.flags["--json"]
	e := engine.New()
	switch mode {
	case "file":
		root := opts.one("--project-root")
		if root == "" {
			root, err = inferRoot(target)
			if err != nil {
				return usageErr(err)
			}
		}
		envName := firstNonEmpty(opts.one("--environment"), opts.one("-e"))
		envPath := ""
		if envName != "" {
			envPath, err = engine.SelectEnvironment(root, envName)
			if err != nil {
				return usageErr(err)
			}
		}
		result, err := e.Validate(target, envPath, root)
		if err != nil {
			return renderValidationError(err, target, jsonOut)
		}
		if err := renderDiagnostics(result.Diagnostics, jsonOut); err != nil {
			return fail(err)
		}
		if !result.OK() {
			return ExitValidationFailed
		}
		return ExitOK
	case "project":
		r, err := e.ValidateProject(target)
		if err != nil {
			return renderValidationError(err, target, jsonOut)
		}
		if err := renderDiagnostics(r.Diagnostics, jsonOut); err != nil {
			return fail(err)
		}
		if !r.OK() {
			return ExitValidationFailed
		}
		return ExitOK
	default:
		fmt.Fprintln(os.Stderr, "unknown validate target:", mode)
		return ExitUsageError
	}
}

func runRun(ctx context.Context, args []string) int {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: veriflow run {suite|discovered} PATH [options]")
		return ExitUsageError
	}
	mode, target := args[0], args[1]
	spec := map[string]bool{"--environment": true, "-e": true, "--project-root": true, "--var-file": true, "--var": true, "--var-from-env": true, "--secret-var-from-env": true, "--max-response-bytes": true, "--run-timeout-ms": true, "--suite-timeout-ms": true, "--test-timeout-ms": true, "--cleanup-timeout-ms": true, "--connect-timeout-ms": true, "--tls-handshake-timeout-ms": true, "--response-header-timeout-ms": true, "--artifacts-root": true, "--exclude-test-id": true, "--exclude-test-name": true, "--exclude-tag": true, "--exclude-suite-id": true, "--exclude-suite-name": true, "--fail-fast": false, "--shard": true, "--fail-if-no-tests": false, "--no-fail-if-no-tests": false, "--ci": false, "--ca-file": true, "--client-cert": true, "--client-key": true, "--proxy": true, "--cookie-jar": false, "--no-cookie-jar": false, "--insecure-skip-tls-verify": false, "--verify-tls": false, "--test-id": true, "--test-name": true, "--tag": true, "--report-json": true, "--report-junit": true, "--report-consolidated-json": true, "--event-jsonl": true, "--json": false, "--suite-id": true, "--suite-name": true, "--report-dir": true}
	opts, err := parseOptions(args[2:], spec)
	if err != nil {
		return usageErr(err)
	}
	for _, pair := range [][2]string{{"--fail-if-no-tests", "--no-fail-if-no-tests"}, {"--cookie-jar", "--no-cookie-jar"}, {"--insecure-skip-tls-verify", "--verify-tls"}} {
		if opts.flags[pair[0]] && opts.flags[pair[1]] {
			return usageErr(fmt.Errorf("%s and %s cannot be used together", pair[0], pair[1]))
		}
	}
	if shard := opts.one("--shard"); shard != "" {
		if mode != "discovered" {
			return usageErr(fmt.Errorf("--shard is only supported with run discovered"))
		}
		if _, _, err := parseShard(shard); err != nil {
			return usageErr(err)
		}
	}
	if mode == "suite" && opts.one("--report-consolidated-json") != "" {
		return usageErr(fmt.Errorf("--report-consolidated-json is only supported with run discovered; use --report-json for one suite"))
	}
	if mode == "discovered" && opts.one("--report-json") != "" {
		return usageErr(fmt.Errorf("--report-json is only supported with run suite; use --report-consolidated-json for discovered runs"))
	}
	switch mode {
	case "suite":
		return runSingleSuite(ctx, target, opts)
	case "discovered":
		return runDiscovered(ctx, target, opts)
	default:
		fmt.Fprintln(os.Stderr, "unknown run target:", mode)
		return ExitUsageError
	}
}

func runSingleSuite(ctx context.Context, suite string, opts optionSet) int {
	root := opts.one("--project-root")
	var err error
	if root == "" {
		root, err = inferRoot(suite)
		if err != nil {
			return usageErr(err)
		}
	}
	opts, configDiagnostics := applyProjectConfig(root, opts)
	if hasDiagnosticErrors(configDiagnostics) {
		return finishValidationFailure(filepath.Base(suite), configDiagnostics, opts, false)
	}

	var initialReportingFailure error
	if err := prepareEventJSONL(opts.one("--event-jsonl")); err != nil {
		initialReportingFailure = fmt.Errorf("prepare event JSONL: %w", err)
	}
	ctx, cancel, timeoutErr := applyRunTimeout(ctx, opts)
	if timeoutErr != nil {
		return usageErr(timeoutErr)
	}
	defer cancel()

	envName := firstNonEmpty(opts.one("--environment"), opts.one("-e"))
	envPath := ""
	if envName != "" {
		envPath, err = engine.SelectEnvironment(root, envName)
		if err != nil {
			return usageErr(err)
		}
	}
	validation, err := engine.New().Validate(suite, envPath, root)
	if err != nil {
		if d, ok := engine.DiagnosticFromError(err, suite); ok {
			return finishValidationFailure(filepath.Base(suite), []engine.Diagnostic{d}, opts, false)
		}
		return fail(err)
	}
	if !validation.OK() {
		name := filepath.Base(suite)
		if doc, loadErr := engine.New().Load(suite); loadErr == nil {
			if typed, ok := doc.Typed.(engine.TestSuiteSpec); ok && typed.Info.Name != "" {
				name = typed.Info.Name
			}
		}
		return finishValidationFailure(name, validation.Diagnostics, opts, false)
	}
	runOpts, err := buildRunnerOptions(root, opts)
	if err != nil {
		if diagnostic, ok := engine.DiagnosticFromError(err, ""); ok {
			return finishValidationFailure(filepath.Base(suite), []engine.Diagnostic{diagnostic}, opts, false)
		}
		return usageErr(err)
	}
	runnerEngine, err := buildEngine(opts)
	if err != nil {
		return usageErr(err)
	}
	eventJSONLPath := opts.one("--event-jsonl")
	if initialReportingFailure != nil {
		eventJSONLPath = ""
	}
	reporter := &engine.LiveReporter{Quiet: opts.flags["--json"], EventJSONLPath: eventJSONLPath}
	result, runErr := runnerEngine.Run(ctx, suite, envPath, reporter, runOpts)
	if result.SchemaVersion == "" {
		result = failedSuiteResult(filepath.Base(suite), nil)
	}

	var invocationFailure *engine.RuntimeDiagnostic
	exitCode := ExitOK
	if initialReportingFailure != nil {
		d := engine.NewRuntimeDiagnostic("reporter-error", initialReportingFailure.Error())
		invocationFailure = &d
		exitCode = ExitInternalError
	} else if runErr != nil {
		d := engine.RuntimeDiagnosticFromError(runErr)
		invocationFailure = &d
		exitCode = runtimeExitCode(runErr, d)
	} else if failIfNoTests(opts) && len(result.Tests) == 0 {
		d := engine.NewRuntimeDiagnostic("no-tests-selected", "no tests matched the requested selection")
		invocationFailure = &d
		exitCode = ExitRuntimeFailed
	} else if result.ValidationFailed {
		exitCode = ExitValidationFailed
	} else if result.Status == engine.Failed {
		exitCode = ExitRuntimeFailed
	}
	if invocationFailure != nil {
		result = markSuiteInvocationFailure(result, *invocationFailure)
	}

	result, reportErr := writeSingleRunReports(result, invocationFailure, opts)
	if reportErr != nil {
		d := engine.NewRuntimeDiagnostic("report-write-error", reportErr.Error())
		invocationFailure = &d
		result = markSuiteInvocationFailure(result, d)
		// Best effort: rewrite every writable report so successful destinations also
		// reflect the reporting failure. A permanently failing destination is still
		// surfaced by process exit 5.
		_, _ = writeSingleRunReports(result, &d, opts)
		exitCode = ExitInternalError
	}
	if opts.flags["--json"] {
		if err := printJSON(result); err != nil {
			return fail(fmt.Errorf("serialize final suite result: %w", err))
		}
	} else {
		renderSuiteSummary(result, false)
		if invocationFailure != nil {
			fmt.Fprintf(os.Stderr, "error: %s [%s] %s\n", invocationFailure.Code, invocationFailure.Name, invocationFailure.Message)
		}
		if reportErr != nil {
			fmt.Fprintf(os.Stderr, "error: report output failed: %v\n", reportErr)
		}
	}
	return exitCode
}

func runDiscovered(ctx context.Context, root string, opts optionSet) int {
	e := engine.New()
	d, err := e.Discover(root)
	if err != nil {
		if diagnostic, ok := engine.DiagnosticFromError(err, root); ok {
			return finishValidationFailure("project-validation", []engine.Diagnostic{diagnostic}, opts, true)
		}
		return fail(err)
	}
	opts, configDiagnostics := applyProjectConfig(d.ProjectRoot, opts)
	if hasDiagnosticErrors(configDiagnostics) {
		return finishValidationFailure("project-validation", configDiagnostics, opts, true)
	}
	var initialReportingFailure error
	if err := prepareEventJSONL(opts.one("--event-jsonl")); err != nil {
		initialReportingFailure = fmt.Errorf("prepare event JSONL: %w", err)
	}
	ctx, cancel, err := applyRunTimeout(ctx, opts)
	if err != nil {
		return usageErr(err)
	}
	defer cancel()

	envName := firstNonEmpty(opts.one("--environment"), opts.one("-e"))
	envPath := ""
	if envName != "" {
		envPath, err = engine.SelectEnvironment(d.ProjectRoot, envName)
		if err != nil {
			return usageErr(err)
		}
	}
	projectValidation, err := e.ValidateProject(d.ProjectRoot)
	if err != nil {
		if diagnostic, ok := engine.DiagnosticFromError(err, d.ProjectRoot); ok {
			return finishValidationFailure("project-validation", []engine.Diagnostic{diagnostic}, opts, true)
		}
		return fail(err)
	}
	if !projectValidation.OK() {
		return finishValidationFailure("project-validation", projectValidation.Diagnostics, opts, true)
	}
	runOpts, err := buildRunnerOptions(d.ProjectRoot, opts)
	if err != nil {
		if diagnostic, ok := engine.DiagnosticFromError(err, ""); ok {
			return finishValidationFailure("project-validation", []engine.Diagnostic{diagnostic}, opts, true)
		}
		return usageErr(err)
	}
	runnerEngine, err := buildEngine(opts)
	if err != nil {
		return usageErr(err)
	}

	suiteIDs := toSet(opts.values["--suite-id"])
	suiteNames := toSet(opts.values["--suite-name"])
	excludeSuiteIDs := toSet(opts.values["--exclude-suite-id"])
	excludeSuiteNames := toSet(opts.values["--exclude-suite-name"])
	results := []engine.SuiteResult{}
	validationFailed, overallFailed := false, false
	var executionFailure error
	var reportingFailure error = initialReportingFailure
	reportingFailureName := ""
	if initialReportingFailure != nil {
		reportingFailureName = "reporter-error"
	}
	reporterBroken := initialReportingFailure != nil
	for _, suitePath := range d.Suites {
		doc, err := e.Load(suitePath)
		if err != nil {
			if diagnostic, ok := engine.DiagnosticFromError(err, suitePath); ok {
				result := engine.ValidationFailureResult(filepath.Base(suitePath), []engine.Diagnostic{diagnostic})
				results = append(results, result)
				validationFailed = true
				break
			}
			executionFailure = err
			break
		}
		typed, _ := doc.Typed.(engine.TestSuiteSpec)
		name := typed.Info.Name
		if name == "" {
			name = typed.Metadata.Name
		}
		if name == "" {
			name = strings.TrimSuffix(filepath.Base(suitePath), filepath.Ext(suitePath))
		}
		id := typed.Metadata.ID
		if len(suiteIDs) > 0 && !suiteIDs[id] {
			continue
		}
		if len(suiteNames) > 0 && !suiteNames[name] {
			continue
		}
		if excludeSuiteIDs[id] || excludeSuiteNames[name] {
			continue
		}
		if shard := opts.one("--shard"); shard != "" {
			index, total, _ := parseShard(shard)
			if !suiteInShard(d.ProjectRoot, suitePath, index, total) {
				continue
			}
		}
		if !opts.flags["--json"] {
			fmt.Printf("\n=== Run %s ===\n", name)
		}
		eventJSONLPath := opts.one("--event-jsonl")
		if reporterBroken {
			eventJSONLPath = ""
		}
		reporter := &engine.LiveReporter{Quiet: opts.flags["--json"] || reporterBroken, EventJSONLPath: eventJSONLPath}
		result, runErr := runnerEngine.Run(ctx, suitePath, envPath, reporter, runOpts)
		if result.SchemaVersion != "" {
			results = append(results, result)
			if !opts.flags["--json"] {
				renderSuiteSummary(result, false)
			}
			if dir := opts.one("--report-dir"); dir != "" {
				safe := strings.NewReplacer("/", "_", "\\", "_", " ", "_").Replace(name)
				if err := writeJSON(filepath.Join(dir, safe+".json"), result); err != nil && reportingFailure == nil {
					reportingFailure = fmt.Errorf("write suite report: %w", err)
					reportingFailureName = "report-write-error"
				}
			}
			overallFailed = overallFailed || result.Status == engine.Failed
			validationFailed = validationFailed || result.ValidationFailed
		}
		if runErr != nil {
			diagnostic := engine.RuntimeDiagnosticFromError(runErr)
			if diagnostic.Name == "reporter-error" {
				if reportingFailure == nil {
					reportingFailure = runErr
					reportingFailureName = "reporter-error"
				}
				reporterBroken = true
			} else {
				executionFailure = runErr
				break
			}
		}
		if opts.flags["--fail-fast"] && (result.Status == engine.Failed || result.ValidationFailed) {
			break
		}
	}

	var invocationFailure *engine.RuntimeDiagnostic
	exitCode := ExitOK
	base := engine.Consolidate(results)
	if reportingFailure != nil {
		if reportingFailureName == "" {
			reportingFailureName = "report-write-error"
		}
		d := engine.NewRuntimeDiagnostic(reportingFailureName, reportingFailure.Error())
		invocationFailure = &d
		exitCode = ExitInternalError
	} else if executionFailure != nil {
		d := engine.RuntimeDiagnosticFromError(executionFailure)
		invocationFailure = &d
		exitCode = runtimeExitCode(executionFailure, d)
	} else if failIfNoTests(opts) && (len(results) == 0 || base.TotalTests == 0) {
		d := engine.NewRuntimeDiagnostic("no-tests-selected", "no tests matched the requested selection")
		invocationFailure = &d
		exitCode = ExitRuntimeFailed
	} else if validationFailed {
		exitCode = ExitValidationFailed
	} else if overallFailed {
		exitCode = ExitRuntimeFailed
	}
	report := engine.BuildRunReport(results, invocationFailure)
	report, reportErr := writeDiscoveredRunReports(report, opts)
	if reportErr != nil {
		d := engine.NewRuntimeDiagnostic("report-write-error", reportErr.Error())
		report = engine.BuildRunReport(results, &d)
		_, _ = writeDiscoveredRunReports(report, opts)
		invocationFailure = &d
		exitCode = ExitInternalError
	}
	if opts.flags["--json"] {
		if err := printJSON(report); err != nil {
			return fail(fmt.Errorf("serialize final discovered run result: %w", err))
		}
	} else {
		renderConsolidated(report.Result, false)
		if invocationFailure != nil {
			fmt.Fprintf(os.Stderr, "error: %s [%s] %s\n", invocationFailure.Code, invocationFailure.Name, invocationFailure.Message)
		}
		if reportErr != nil {
			fmt.Fprintf(os.Stderr, "error: report output failed: %v\n", reportErr)
		}
	}
	return exitCode
}

func parseShard(raw string) (index, total int, err error) {
	parts := strings.Split(raw, "/")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("--shard must use INDEX/TOTAL, for example 1/4")
	}
	index, err = strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("--shard index must be an integer")
	}
	total, err = strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("--shard total must be an integer")
	}
	if total <= 0 || index <= 0 || index > total {
		return 0, 0, fmt.Errorf("--shard must satisfy 1 <= INDEX <= TOTAL")
	}
	return index, total, nil
}

func suiteInShard(projectRoot, suitePath string, index, total int) bool {
	rel, err := filepath.Rel(projectRoot, suitePath)
	if err != nil {
		rel = suitePath
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(filepath.ToSlash(rel)))
	return int(h.Sum64()%uint64(total)) == index-1
}

func buildEngine(opts optionSet) (*engine.Engine, error) {
	parseNonNegative := func(flag string) (int, error) {
		raw := opts.one(flag)
		if raw == "" {
			return 0, nil
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("%s must be a non-negative integer (0 disables it)", flag)
		}
		return n, nil
	}
	connectTimeoutMS, err := parseNonNegative("--connect-timeout-ms")
	if err != nil {
		return nil, err
	}
	tlsHandshakeTimeoutMS, err := parseNonNegative("--tls-handshake-timeout-ms")
	if err != nil {
		return nil, err
	}
	responseHeaderTimeoutMS, err := parseNonNegative("--response-header-timeout-ms")
	if err != nil {
		return nil, err
	}
	config := engine.HTTPClientConfig{
		CAFile: opts.one("--ca-file"), ClientCertFile: opts.one("--client-cert"), ClientKeyFile: opts.one("--client-key"), ProxyURL: opts.one("--proxy"),
		InsecureSkipVerify: opts.flags["--insecure-skip-tls-verify"], EnableCookieJar: opts.flags["--cookie-jar"],
		ConnectTimeoutMS: connectTimeoutMS, TLSHandshakeTimeoutMS: tlsHandshakeTimeoutMS, ResponseHeaderTimeoutMS: responseHeaderTimeoutMS,
	}
	client, err := engine.NewHTTPClient(config)
	if err != nil {
		return nil, err
	}
	e := engine.New()
	e.HTTPClient = client
	return e, nil
}

func buildRunnerOptions(root string, opts optionSet) (engine.RunnerOptions, error) {
	o := engine.DefaultRunnerOptions()
	o.ProjectRoot = root
	o.ArtifactsRoot = opts.one("--artifacts-root")
	o.TestIDs = toSet(opts.values["--test-id"])
	o.TestNames = toSet(opts.values["--test-name"])
	o.Tags = toSet(opts.values["--tag"])
	o.ExcludeTestIDs = toSet(opts.values["--exclude-test-id"])
	o.ExcludeTestNames = toSet(opts.values["--exclude-test-name"])
	o.ExcludeTags = toSet(opts.values["--exclude-tag"])
	vars := map[string]any{}
	for _, vf := range opts.values["--var-file"] {
		m, err := engine.ParseVariableFile(vf)
		if err != nil {
			return o, err
		}
		vars = merge(vars, m)
	}
	adhoc, err := engine.ParseAdHocVariables(opts.values["--var"])
	if err != nil {
		return o, err
	}
	o.VariableOverrides = merge(vars, adhoc)
	for _, item := range opts.values["--var-from-env"] {
		name, envName := splitEnvImport(item)
		value, ok := os.LookupEnv(envName)
		if !ok {
			return o, fmt.Errorf("environment variable %s is not set", envName)
		}
		if err := setDottedValue(o.VariableOverrides, name, parseImportedValue(value)); err != nil {
			return o, err
		}
	}
	for _, item := range opts.values["--secret-var-from-env"] {
		name, envName := splitEnvImport(item)
		value, ok := os.LookupEnv(envName)
		if !ok {
			return o, fmt.Errorf("environment variable %s is not set", envName)
		}
		if err := setDottedValue(o.VariableOverrides, name, value); err != nil {
			return o, err
		}
		o.SensitiveVariables[name] = true
	}
	if raw := opts.one("--max-response-bytes"); raw != "" {
		n, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || n <= 0 {
			return o, fmt.Errorf("--max-response-bytes must be a positive integer")
		}
		o.MaxResponseBytes = n
	}
	for flagName, target := range map[string]*int{"--suite-timeout-ms": &o.SuiteTimeoutMS, "--test-timeout-ms": &o.TestTimeoutMS, "--cleanup-timeout-ms": &o.CleanupTimeoutMS} {
		if raw := opts.one(flagName); raw != "" {
			n, err := strconv.Atoi(raw)
			if err != nil || n <= 0 {
				return o, fmt.Errorf("%s must be a positive integer", flagName)
			}
			*target = n
		}
	}
	return o, nil
}

func parseImportedValue(raw string) any {
	var v any
	if json.Unmarshal([]byte(raw), &v) == nil {
		return v
	}
	return raw
}
func splitEnvImport(item string) (string, string) {
	if i := strings.IndexByte(item, '='); i > 0 {
		return item[:i], item[i+1:]
	}
	return item, item
}

func setDottedValue(root map[string]any, path string, value any) error {
	parts := strings.Split(path, ".")
	if path == "" || len(parts) == 0 {
		return fmt.Errorf("empty variable name")
	}
	cur := root
	for _, p := range parts[:len(parts)-1] {
		if p == "" {
			return fmt.Errorf("invalid variable path %q", path)
		}
		if existing, ok := cur[p]; ok {
			m, ok := existing.(map[string]any)
			if !ok {
				return fmt.Errorf("conflicting variable path %q", path)
			}
			cur = m
		} else {
			m := map[string]any{}
			cur[p] = m
			cur = m
		}
	}
	last := parts[len(parts)-1]
	if last == "" {
		return fmt.Errorf("invalid variable path %q", path)
	}
	cur[last] = value
	return nil
}

func renderDiagnostics(ds []engine.Diagnostic, jsonOut bool) error {
	if jsonOut {
		return printJSON(ds)
	}
	if len(ds) == 0 {
		fmt.Println("No diagnostics.")
		return nil
	}
	fmt.Printf("%-8s %-8s %-24s %-60s %s\n", "SEVERITY", "CODE", "NAME", "MESSAGE", "LOCATION")
	for _, d := range ds {
		loc := d.Location.File
		if d.Location.Line > 0 {
			loc = fmt.Sprintf("%s:%d:%d", loc, d.Location.Line, d.Location.Column)
		}
		fmt.Printf("%-8s %-8s %-24s %-60s %s\n", d.Severity, d.Code, d.Name, d.Message, loc)
	}
	return nil
}
func renderSuiteSummary(r engine.SuiteResult, jsonOut bool) error {
	if jsonOut {
		return printJSON(r)
	}
	fmt.Println("\nSuite Summary")
	fmt.Printf("  Status:      %s\n  Status code: %d\n  Passed:      %d\n  Failed:      %d\n  Skipped:     %d\n  Duration:    %.2f ms\n", r.Status, r.StatusCode, r.PassedCount, r.FailedCount, r.SkippedCount, r.DurationMS)
	if len(r.Tests) > 0 {
		fmt.Println("  Tests:")
		for _, t := range r.Tests {
			fmt.Printf("    %-24s %-8s %.2f ms\n", t.ID, t.Status, t.DurationMS)
		}
	}
	printedFailures := false
	for _, t := range r.Tests {
		if t.Status != engine.Failed {
			continue
		}
		if !printedFailures {
			fmt.Println("  Failures:")
			printedFailures = true
		}
		for _, step := range t.Steps {
			if step.Status != engine.Failed {
				continue
			}
			fmt.Printf("    %s / %s\n", t.ID, step.ID)
			if step.RuntimeError != nil {
				fmt.Printf("      %s [%s]: %s\n", step.RuntimeError.Code, step.RuntimeError.Name, step.RuntimeError.Message)
			} else if step.Error != "" {
				fmt.Printf("      error: %s\n", step.Error)
			}
			for _, a := range step.Assertions {
				if !a.Passed {
					fmt.Printf("      assertion: %s.%s expected=%v actual=%v\n", a.Target, a.Operator, a.Expected, a.Actual)
				}
			}
			for _, x := range step.Extractions {
				if !x.Passed {
					fmt.Printf("      extraction: %s %s\n", x.Name, x.Message)
				}
			}
		}
	}
	return nil
}
func renderConsolidated(r engine.ConsolidatedResult, jsonOut bool) error {
	if jsonOut {
		return printJSON(map[string]any{"type": "consolidatedSummary", "result": r})
	}
	fmt.Println("\n=== Consolidated Test Results ===")
	fmt.Printf("  Suites:      %d\n  Total tests: %d\n  Passed:      %d\n  Failed:      %d\n  Skipped:     %d\n", r.Suites, r.TotalTests, r.Passed, r.Failed, r.Skipped)
	if r.ValidationFailedSuites > 0 {
		fmt.Printf("  Invalid:     %d suite(s)\n", r.ValidationFailedSuites)
	}
	fmt.Printf("  Duration:    %.2f ms\n  Status:      %s\n  Status code: %d\n", r.DurationMS, r.OverallStatus, r.StatusCode)
	return nil
}

type optionSet struct {
	flags  map[string]bool
	values map[string][]string
}

func (o optionSet) one(k string) string {
	v := o.values[k]
	if len(v) == 0 {
		return ""
	}
	return v[len(v)-1]
}
func parseOptions(args []string, spec map[string]bool) (optionSet, error) {
	o := optionSet{flags: map[string]bool{}, values: map[string][]string{}}
	for i := 0; i < len(args); i++ {
		a := args[i]
		needs, ok := spec[a]
		if !ok {
			return o, fmt.Errorf("unknown option %s", a)
		}
		if needs {
			if i+1 >= len(args) {
				return o, fmt.Errorf("%s requires a value", a)
			}
			i++
			o.values[a] = append(o.values[a], args[i])
		} else {
			o.flags[a] = true
		}
	}
	return o, nil
}
func hasFlag(args []string, flag string) bool {
	for _, a := range args {
		if a == flag {
			return true
		}
	}
	return false
}
func toSet(items []string) map[string]bool {
	m := map[string]bool{}
	for _, x := range items {
		m[x] = true
	}
	return m
}
func merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		if am, ok := out[k].(map[string]any); ok {
			if bm, ok2 := v.(map[string]any); ok2 {
				out[k] = merge(am, bm)
				continue
			}
		}
		out[k] = v
	}
	return out
}
func writeJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}
func printJSON(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Println(string(b))
	return err
}

func prepareEventJSONL(path string) error {
	if path == "" {
		return nil
	}
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(path, nil, 0o644)
}

func appendValidationEvents(path string, diagnostics []engine.Diagnostic) error {
	if path == "" {
		return nil
	}
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, d := range diagnostics {
		event := engine.NewEvent("validation.error", map[string]any{"code": d.Code, "name": d.Name, "message": d.Message, "file": d.Location.File, "path": d.Location.DocumentPath, "line": d.Location.Line, "column": d.Location.Column})
		if err := enc.Encode(event); err != nil {
			return err
		}
	}
	return nil
}

func finishValidationFailure(name string, diagnostics []engine.Diagnostic, opts optionSet, discovered bool) int {
	result := engine.ValidationFailureResult(name, diagnostics)

	var invocationFailure *engine.RuntimeDiagnostic
	if err := appendValidationEvents(opts.one("--event-jsonl"), diagnostics); err != nil {
		d := engine.NewRuntimeDiagnostic("reporter-error", fmt.Sprintf("write validation event stream: %v", err))
		invocationFailure = &d
		result = markSuiteInvocationFailure(result, d)
	}

	var reportErr error
	remember := func(err error) {
		if err != nil && reportErr == nil {
			reportErr = err
		}
	}
	writeReports := func(failure *engine.RuntimeDiagnostic) {
		if !discovered {
			if path := opts.one("--report-json"); path != "" {
				remember(writeJSON(path, result))
			}
		} else {
			if dir := opts.one("--report-dir"); dir != "" {
				remember(writeJSON(filepath.Join(dir, "project-validation.json"), result))
			}
			if path := opts.one("--report-consolidated-json"); path != "" {
				remember(engine.WriteRunReportValue(path, engine.BuildRunReport([]engine.SuiteResult{result}, failure)))
			}
		}
		if path := opts.one("--report-junit"); path != "" {
			remember(engine.WriteJUnitWithFailure(path, []engine.SuiteResult{result}, failure))
		}
	}
	writeReports(invocationFailure)

	if reportErr != nil {
		d := engine.NewRuntimeDiagnostic("report-write-error", reportErr.Error())
		invocationFailure = &d
		result = markSuiteInvocationFailure(result, d)
		// Best effort keeps any writable destinations consistent with the final
		// invocation outcome. The original write error still determines exit 5.
		reportErr = nil
		writeReports(&d)
	}

	if opts.flags["--json"] {
		var err error
		if discovered {
			err = printJSON(engine.BuildRunReport([]engine.SuiteResult{result}, invocationFailure))
		} else {
			err = printJSON(result)
		}
		if err != nil {
			return fail(fmt.Errorf("serialize validation result: %w", err))
		}
	} else {
		if err := renderDiagnostics(diagnostics, false); err != nil {
			return fail(err)
		}
		if discovered {
			if err := renderConsolidated(engine.BuildRunReport([]engine.SuiteResult{result}, invocationFailure).Result, false); err != nil {
				return fail(err)
			}
		} else {
			if err := renderSuiteSummary(result, false); err != nil {
				return fail(err)
			}
		}
		if invocationFailure != nil {
			fmt.Fprintf(os.Stderr, "error: %s [%s] %s\n", invocationFailure.Code, invocationFailure.Name, invocationFailure.Message)
		}
	}
	if invocationFailure != nil {
		return ExitInternalError
	}
	return ExitValidationFailed
}

func renderValidationError(err error, fallbackPath string, jsonOut bool) int {
	if d, ok := engine.DiagnosticFromError(err, fallbackPath); ok {
		if renderErr := renderDiagnostics([]engine.Diagnostic{d}, jsonOut); renderErr != nil {
			return fail(renderErr)
		}
		return ExitValidationFailed
	}
	return fail(err)
}

func fail(err error) int {
	if engine.IsUserLoadError(err) {
		fmt.Fprintln(os.Stderr, "validation error:", err)
		return ExitValidationFailed
	}
	fmt.Fprintln(os.Stderr, "error:", err)
	return ExitInternalError
}
func usageErr(err error) int { fmt.Fprintln(os.Stderr, "error:", err); return ExitUsageError }
func executionErr(err error) int {
	fmt.Fprintln(os.Stderr, "error:", err)
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ExitRuntimeFailed
	}
	return ExitInternalError
}
func failIfNoTests(opts optionSet) bool {
	return opts.flags["--fail-if-no-tests"] || opts.flags["--ci"]
}
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
func inferRoot(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", path, err)
	}
	cur := filepath.Dir(abs)
	for {
		if exists(filepath.Join(cur, "suites")) || exists(filepath.Join(cur, "requests")) || exists(filepath.Join(cur, "environments")) {
			return cur, nil
		}
		p := filepath.Dir(cur)
		if p == cur {
			return filepath.Dir(abs), nil
		}
		cur = p
	}
}
func exists(p string) bool { s, err := os.Stat(p); return err == nil && s.IsDir() }
func stringValue(m map[string]any, k string) string {
	if m == nil {
		return ""
	}
	v := m[k]
	if s, ok := v.(string); ok {
		return s
	}
	if v != nil {
		return fmt.Sprint(v)
	}
	return ""
}

// Sorted is used by tests and keeps CLI output deterministic when needed.
func Sorted(items []string) []string {
	out := append([]string{}, items...)
	sort.Strings(out)
	return out
}

func hasDiagnosticErrors(ds []engine.Diagnostic) bool {
	for _, d := range ds {
		if d.IsError() {
			return true
		}
	}
	return false
}

func applyProjectConfig(root string, opts optionSet) (optionSet, []engine.Diagnostic) {
	cfg, diagnostics := engine.LoadProjectConfig(root)
	if hasDiagnosticErrors(diagnostics) {
		return opts, diagnostics
	}
	setValue := func(flag, value string, path bool) {
		if value == "" || opts.one(flag) != "" {
			return
		}
		if path {
			value = engine.ResolveProjectConfigPath(root, value)
		}
		opts.values[flag] = []string{value}
	}
	setPositiveInt := func(flag string, value int64) {
		if value > 0 && opts.one(flag) == "" {
			opts.values[flag] = []string{strconv.FormatInt(value, 10)}
		}
	}
	setPositiveInt("--run-timeout-ms", int64(cfg.Runtime.RunTimeoutMS))
	setPositiveInt("--suite-timeout-ms", int64(cfg.Runtime.SuiteTimeoutMS))
	setPositiveInt("--test-timeout-ms", int64(cfg.Runtime.TestTimeoutMS))
	setPositiveInt("--cleanup-timeout-ms", int64(cfg.Runtime.CleanupTimeoutMS))
	setPositiveInt("--max-response-bytes", cfg.Runtime.MaxResponseBytes)
	setPositiveInt("--connect-timeout-ms", int64(cfg.Network.ConnectTimeoutMS))
	setPositiveInt("--tls-handshake-timeout-ms", int64(cfg.Network.TLSHandshakeTimeoutMS))
	setPositiveInt("--response-header-timeout-ms", int64(cfg.Network.ResponseHeaderTimeoutMS))
	setValue("--artifacts-root", cfg.Runtime.ArtifactsRoot, true)
	setValue("--report-json", cfg.Reports.JSON, true)
	setValue("--report-junit", cfg.Reports.JUnit, true)
	setValue("--report-consolidated-json", cfg.Reports.ConsolidatedJSON, true)
	setValue("--report-dir", cfg.Reports.ReportDir, true)
	setValue("--event-jsonl", cfg.Reports.EventJSONL, true)
	setValue("--ca-file", cfg.Network.CAFile, true)
	setValue("--client-cert", cfg.Network.ClientCert, true)
	setValue("--client-key", cfg.Network.ClientKey, true)
	setValue("--proxy", cfg.Network.Proxy, false)
	if cfg.CI.FailIfNoTests && !opts.flags["--fail-if-no-tests"] && !opts.flags["--no-fail-if-no-tests"] {
		opts.flags["--fail-if-no-tests"] = true
	}
	if cfg.Network.CookieJar && !opts.flags["--cookie-jar"] && !opts.flags["--no-cookie-jar"] {
		opts.flags["--cookie-jar"] = true
	}
	if cfg.Network.InsecureSkipTLSVerify && !opts.flags["--insecure-skip-tls-verify"] && !opts.flags["--verify-tls"] {
		opts.flags["--insecure-skip-tls-verify"] = true
	}
	return opts, diagnostics
}

func failedSuiteResult(name string, failure *engine.RuntimeDiagnostic) engine.SuiteResult {
	r := engine.SuiteResult{SchemaVersion: engine.ReportSchemaVersion, EngineVersion: engine.EngineVersion, Name: name, Status: engine.Failed, StatusCode: -1}
	if failure != nil {
		r.RuntimeError = failure
		r.Error = failure.Message
	}
	return r
}

func markSuiteInvocationFailure(result engine.SuiteResult, failure engine.RuntimeDiagnostic) engine.SuiteResult {
	if result.SchemaVersion == "" {
		result.SchemaVersion = engine.ReportSchemaVersion
	}
	if result.EngineVersion == "" {
		result.EngineVersion = engine.EngineVersion
	}
	result.Status = engine.Failed
	result.StatusCode = -1
	result.RuntimeError = &failure
	if result.Error == "" {
		result.Error = failure.Message
	}
	return result
}

func runtimeExitCode(err error, diagnostic engine.RuntimeDiagnostic) int {
	if diagnostic.Name == "reporter-error" || diagnostic.Name == "report-write-error" || diagnostic.Name == "serialization-error" {
		return ExitInternalError
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ExitRuntimeFailed
	}
	if engine.IsUserLoadError(err) {
		return ExitValidationFailed
	}
	return ExitRuntimeFailed
}

func writeSingleRunReports(result engine.SuiteResult, failure *engine.RuntimeDiagnostic, opts optionSet) (engine.SuiteResult, error) {
	var first error
	if path := opts.one("--report-junit"); path != "" {
		if err := engine.WriteJUnitWithFailure(path, []engine.SuiteResult{result}, failure); err != nil && first == nil {
			first = fmt.Errorf("write JUnit report: %w", err)
		}
	}
	if path := opts.one("--report-json"); path != "" {
		if err := writeJSON(path, result); err != nil && first == nil {
			first = fmt.Errorf("write JSON report: %w", err)
		}
	}
	return result, first
}

func writeDiscoveredRunReports(report engine.RunReport, opts optionSet) (engine.RunReport, error) {
	var first error
	if path := opts.one("--report-junit"); path != "" {
		if err := engine.WriteJUnitWithFailure(path, report.Suites, report.Failure); err != nil && first == nil {
			first = fmt.Errorf("write JUnit report: %w", err)
		}
	}
	if path := opts.one("--report-consolidated-json"); path != "" {
		if err := engine.WriteRunReportValue(path, report); err != nil && first == nil {
			first = fmt.Errorf("write consolidated JSON report: %w", err)
		}
	}
	return report, first
}

func applyRunTimeout(ctx context.Context, opts optionSet) (context.Context, context.CancelFunc, error) {
	raw := opts.one("--run-timeout-ms")
	if raw == "" {
		return ctx, func() {}, nil
	}
	ms, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || ms <= 0 {
		return ctx, func() {}, fmt.Errorf("--run-timeout-ms must be a positive integer")
	}
	child, cancel := context.WithTimeout(ctx, time.Duration(ms)*time.Millisecond)
	return child, cancel, nil
}
