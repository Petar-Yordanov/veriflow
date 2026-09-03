package cli

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestValidateProjectTreatsMalformedYAMLAsValidationFailure(t *testing.T) {
	root := t.TempDir()
	mustWriteCLI(t, filepath.Join(root, "suites", "broken.yml"), "formatVersion: \"1.0\"\nkind: testSuite\ntests:\n  - id: broken\n     bad-indent: true\n")
	if code := RunContext(context.Background(), []string{"validate", "project", root}); code != ExitValidationFailed {
		t.Fatalf("exit=%d want %d", code, ExitValidationFailed)
	}
}

func TestRunStaticValidationFailureUsesValidationExitCode(t *testing.T) {
	root := t.TempDir()
	mustWriteCLI(t, filepath.Join(root, "suites", "bad.yml"), `formatVersion: "1.0"
kind: testSuite
info:
  name: bad
tests:
  - id: bad
    steps:
      - id: bad-file
        request:
          method: POST
          url: http://example.invalid
          bodyFile: ../outside.txt
          bodyFileMode: text
`)
	if code := RunContext(context.Background(), []string{"run", "suite", filepath.Join(root, "suites", "bad.yml"), "--project-root", root}); code != ExitValidationFailed {
		t.Fatalf("exit=%d want %d", code, ExitValidationFailed)
	}
}

func TestMissingEnvironmentImportIsUsageError(t *testing.T) {
	root := t.TempDir()
	mustWriteCLI(t, filepath.Join(root, "suites", "empty.yml"), `formatVersion: "1.0"
kind: testSuite
info:
  name: empty
tests: []
`)
	const name = "VERIFLOW_TEST_MISSING_ENV_48291"
	_ = os.Unsetenv(name)
	if code := RunContext(context.Background(), []string{"run", "suite", filepath.Join(root, "suites", "empty.yml"), "--project-root", root, "--var-from-env", "value=" + name}); code != ExitUsageError {
		t.Fatalf("exit=%d want %d", code, ExitUsageError)
	}
}

func mustWriteCLI(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestRunMalformedSuiteUsesValidationExitCode(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "suites", "broken.yml")
	mustWriteCLI(t, path, "formatVersion: \"1.0\"\nkind: testSuite\ntests:\n  - id: broken\n     bad-indent: true\n")
	if code := RunContext(context.Background(), []string{"run", "suite", path, "--project-root", root}); code != ExitValidationFailed {
		t.Fatalf("exit=%d want %d", code, ExitValidationFailed)
	}
}

func TestCIModeFailsWhenSelectionMatchesNoTests(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "suites", "one.yml")
	mustWriteCLI(t, path, `formatVersion: "1.0"
kind: testSuite
info:
  name: one
tests:
  - id: selected
    tags: [smoke]
    steps:
      - id: wait
        wait:
          forMs: 1
`)
	code := RunContext(context.Background(), []string{"run", "suite", path, "--project-root", root, "--test-id", "does-not-exist", "--ci"})
	if code != ExitRuntimeFailed {
		t.Fatalf("exit=%d want %d", code, ExitRuntimeFailed)
	}
}

func TestRunTimeoutCancelsWait(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "suites", "slow.yml")
	mustWriteCLI(t, path, `formatVersion: "1.0"
kind: testSuite
info:
  name: slow
tests:
  - id: slow
    steps:
      - id: wait
        wait:
          forMs: 1000
`)
	started := time.Now()
	code := RunContext(context.Background(), []string{"run", "suite", path, "--project-root", root, "--run-timeout-ms", "20"})
	if code != ExitRuntimeFailed {
		t.Fatalf("exit=%d want %d", code, ExitRuntimeFailed)
	}
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("run timeout took too long: %s", elapsed)
	}
}

func TestSchemaCommandWritesSchema(t *testing.T) {
	out := filepath.Join(t.TempDir(), "schema.json")
	if code := RunContext(context.Background(), []string{"schema", "--output", out}); code != ExitOK {
		t.Fatalf("exit=%d", code)
	}
	b, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"$schema"`) || !strings.Contains(string(b), `"requestDefinition"`) {
		t.Fatalf("unexpected schema output: %s", b)
	}
}

func TestShardParsingAndAssignmentAreDeterministic(t *testing.T) {
	index, total, err := parseShard("2/4")
	if err != nil || index != 2 || total != 4 {
		t.Fatalf("unexpected shard parse: index=%d total=%d err=%v", index, total, err)
	}
	if _, _, err := parseShard("0/4"); err == nil {
		t.Fatal("expected invalid zero shard index to fail")
	}
	root := t.TempDir()
	path := filepath.Join(root, "suites", "smoke.yml")
	first := -1
	for shard := 1; shard <= 4; shard++ {
		if suiteInShard(root, path, shard, 4) {
			if first != -1 {
				t.Fatalf("suite assigned to multiple shards: %d and %d", first, shard)
			}
			first = shard
		}
	}
	if first == -1 {
		t.Fatal("suite was not assigned to any shard")
	}
	if !suiteInShard(root, path, first, 4) {
		t.Fatal("shard assignment changed between calls")
	}
}

func TestShardRejectedForSingleSuiteRun(t *testing.T) {
	code := Run([]string{"run", "suite", "missing.yml", "--shard", "1/2"})
	if code != ExitUsageError {
		t.Fatalf("expected usage exit %d, got %d", ExitUsageError, code)
	}
}

func TestDiscoverTestsCommand(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "suites"), 0o755); err != nil {
		t.Fatal(err)
	}
	suite := `formatVersion: "1.0"
kind: testSuite
id: sample
info:
  name: Sample
tests:
  - id: one
    name: First
    tags: [smoke, ci]
    steps:
      - id: wait
        wait:
          forMs: 1
`
	if err := os.WriteFile(filepath.Join(root, "suites", "sample.yml"), []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := Run([]string{"discover", "tests", root, "--json"}); code != ExitOK {
		t.Fatalf("expected discover tests exit %d, got %d", ExitOK, code)
	}
}

func TestFailedSuiteWritesReportsBeforeReturningPipelineFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	root := t.TempDir()
	suite := filepath.Join(root, "suites", "fails.yml")
	mustWriteCLI(t, suite, `formatVersion: "1.0"
kind: testSuite
info:
  name: fails
tests:
  - id: fails
    steps:
      - id: request
        request:
          method: GET
          url: "`+server.URL+`"
        expect:
          status: 201
`)
	report := filepath.Join(root, "out", "result.json")
	junit := filepath.Join(root, "out", "junit.xml")
	code := RunContext(context.Background(), []string{"run", "suite", suite, "--project-root", root, "--report-json", report, "--report-junit", junit})
	if code != ExitRuntimeFailed {
		t.Fatalf("exit=%d want %d", code, ExitRuntimeFailed)
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("JSON report must exist before failure status is returned: %v", err)
	}
	if !strings.Contains(string(data), `"statusCode": -1`) || !strings.Contains(string(data), `"status": "failed"`) {
		t.Fatalf("failed logical status missing from report: %s", data)
	}
	if _, err := os.Stat(junit); err != nil {
		t.Fatalf("JUnit report must exist before failure status is returned: %v", err)
	}
}

func TestSuccessfulSuiteReportHasZeroLogicalStatus(t *testing.T) {
	root := t.TempDir()
	suite := filepath.Join(root, "suites", "pass.yml")
	mustWriteCLI(t, suite, `formatVersion: "1.0"
kind: testSuite
info:
  name: pass
tests:
  - id: pass
    steps:
      - id: wait
        wait:
          forMs: 1
`)
	report := filepath.Join(root, "result.json")
	if code := RunContext(context.Background(), []string{"run", "suite", suite, "--project-root", root, "--report-json", report}); code != ExitOK {
		t.Fatalf("exit=%d want %d", code, ExitOK)
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"statusCode": 0`) {
		t.Fatalf("successful logical status missing from report: %s", data)
	}
}

func TestEventSinkFailureStillWritesRequestedFinalReport(t *testing.T) {
	root := t.TempDir()
	suite := filepath.Join(root, "suites", "pass.yml")
	mustWriteCLI(t, suite, `formatVersion: "1.0"
kind: testSuite
info:
  name: pass
tests:
  - id: pass
    steps:
      - id: wait
        wait:
          forMs: 1
`)
	blocked := filepath.Join(root, "blocked")
	mustWriteCLI(t, blocked, "not a directory")
	events := filepath.Join(blocked, "events.jsonl")
	report := filepath.Join(root, "final.json")
	code := RunContext(context.Background(), []string{"run", "suite", suite, "--project-root", root, "--event-jsonl", events, "--report-json", report})
	if code == ExitOK {
		t.Fatal("event sink failure must fail the pipeline invocation")
	}
	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatalf("final report should still be attempted after event sink failure: %v", err)
	}
	if !strings.Contains(string(data), `"statusCode": -1`) || !strings.Contains(string(data), `"passedCount": 1`) || !strings.Contains(string(data), `"name": "reporter-error"`) {
		t.Fatalf("final report must preserve the passed test while marking the invocation failed: %s", data)
	}
}

func TestProjectConfigProvidesTimeoutAndCLIOverridesIt(t *testing.T) {
	root := t.TempDir()
	mustWriteCLI(t, filepath.Join(root, "veriflow.yml"), `runtime:
  testTimeoutMs: 20
  cleanupTimeoutMs: 100
`)
	suite := filepath.Join(root, "suites", "slow.yml")
	mustWriteCLI(t, suite, `formatVersion: "1.0"
kind: testSuite
info:
  name: slow
tests:
  - id: slow
    steps:
      - id: wait
        wait:
          forMs: 60
`)
	if code := RunContext(context.Background(), []string{"run", "suite", suite, "--project-root", root}); code != ExitRuntimeFailed {
		t.Fatalf("project test timeout exit=%d want %d", code, ExitRuntimeFailed)
	}
	if code := RunContext(context.Background(), []string{"run", "suite", suite, "--project-root", root, "--test-timeout-ms", "200"}); code != ExitOK {
		t.Fatalf("CLI timeout must override project config, exit=%d", code)
	}
}

func TestMalformedUserSpecJSONDiagnosticUsesStableCode(t *testing.T) {
	root := t.TempDir()
	suite := filepath.Join(root, "suites", "missing-version.yml")
	mustWriteCLI(t, suite, `kind: testSuite
tests: []
`)
	output := captureStdoutCLI(t, func() {
		if code := RunContext(context.Background(), []string{"validate", "file", suite, "--project-root", root, "--json"}); code != ExitValidationFailed {
			t.Fatalf("exit=%d want %d", code, ExitValidationFailed)
		}
	})
	if !strings.Contains(output, `"code":"VF1008"`) || !strings.Contains(output, `"name":"missing-format-version"`) {
		t.Fatalf("structured stable diagnostic missing: %s", output)
	}
}

func captureStdoutCLI(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	_ = w.Close()
	data, err := io.ReadAll(r)
	_ = r.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestDiscoveredRunWritesAggregateReportsThenReturnsFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	root := t.TempDir()
	mustWriteCLI(t, filepath.Join(root, "suites", "01-pass.yml"), `formatVersion: "1.0"
kind: testSuite
id: pass
info:
  name: pass
tests:
  - id: pass
    steps:
      - id: call
        request:
          method: GET
          url: "`+server.URL+`"
        expect:
          status: 200
`)
	mustWriteCLI(t, filepath.Join(root, "suites", "02-fail.yml"), `formatVersion: "1.0"
kind: testSuite
id: fail
info:
  name: fail
tests:
  - id: fail
    steps:
      - id: call
        request:
          method: GET
          url: "`+server.URL+`"
        expect:
          status: 201
`)
	consolidated := filepath.Join(root, "out", "run.json")
	junit := filepath.Join(root, "out", "junit.xml")
	reportDir := filepath.Join(root, "out", "suites")
	events := filepath.Join(root, "out", "events.jsonl")
	code := RunContext(context.Background(), []string{
		"run", "discovered", root,
		"--report-consolidated-json", consolidated,
		"--report-junit", junit,
		"--report-dir", reportDir,
		"--event-jsonl", events,
	})
	if code != ExitRuntimeFailed {
		t.Fatalf("exit=%d want %d", code, ExitRuntimeFailed)
	}
	data, err := os.ReadFile(consolidated)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"statusCode": -1`) || !strings.Contains(string(data), `"totalTests": 2`) {
		t.Fatalf("aggregate failure status/totals missing: %s", data)
	}
	for _, path := range []string{junit, events, filepath.Join(reportDir, "pass.json"), filepath.Join(reportDir, "fail.json")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected pipeline output %s: %v", path, err)
		}
	}
}

func TestEnvironmentImportsSupportJSONAndNestedPaths(t *testing.T) {
	t.Setenv("VF_IMPORTED_JSON", `{"enabled":true,"count":2}`)
	t.Setenv("VF_SHORT_SECRET", "42")
	opts := optionSet{flags: map[string]bool{}, values: map[string][]string{
		"--var-from-env":        {"config=VF_IMPORTED_JSON"},
		"--secret-var-from-env": {"auth.pin=VF_SHORT_SECRET"},
	}}
	built, err := buildRunnerOptions(t.TempDir(), opts)
	if err != nil {
		t.Fatal(err)
	}
	config, ok := built.VariableOverrides["config"].(map[string]any)
	if !ok || config["enabled"] != true {
		t.Fatalf("JSON env import not parsed: %#v", built.VariableOverrides)
	}
	auth, ok := built.VariableOverrides["auth"].(map[string]any)
	if !ok || auth["pin"] != "42" || !built.SensitiveVariables["auth.pin"] {
		t.Fatalf("nested secret import missing: vars=%#v sensitive=%#v", built.VariableOverrides, built.SensitiveVariables)
	}
	if err := setDottedValue(map[string]any{"a": "scalar"}, "a.b", 1); err == nil {
		t.Fatal("conflicting dotted variable path must fail")
	}
}

func TestConfigSchemaCommandAndDiscoverErrorsAreStructured(t *testing.T) {
	out := filepath.Join(t.TempDir(), "project.schema.json")
	if code := RunContext(context.Background(), []string{"schema", "config", "--output", out}); code != ExitOK {
		t.Fatalf("schema config exit=%d", code)
	}
	b, err := os.ReadFile(out)
	if err != nil || !strings.Contains(string(b), `"Veriflow project configuration 1.0"`) {
		t.Fatalf("unexpected config schema: %q err=%v", b, err)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	output := captureStdoutCLI(t, func() {
		if code := RunContext(context.Background(), []string{"discover", "suites", missing, "--json"}); code != ExitValidationFailed {
			t.Fatalf("discover missing root exit=%d", code)
		}
	})
	if !strings.Contains(output, `"code":"VF1604"`) || !strings.Contains(output, `"name":"project-root-not-found"`) {
		t.Fatalf("missing structured discover diagnostic: %s", output)
	}
	if code := RunContext(context.Background(), []string{"discover", "suites", t.TempDir(), "--bogus"}); code != ExitUsageError {
		t.Fatalf("unknown discover option exit=%d", code)
	}
}

func TestValidationFailureWritesRequestedReportsBeforeExit(t *testing.T) {
	root := t.TempDir()
	suite := filepath.Join(root, "suites", "bad.yml")
	mustWriteCLI(t, suite, `formatVersion: "1.0"
kind: testSuite
info:
  name: bad
unknownField: true
tests: []
`)
	jsonReport := filepath.Join(root, "out", "suite.json")
	junit := filepath.Join(root, "out", "junit.xml")
	events := filepath.Join(root, "out", "events.jsonl")
	code := RunContext(context.Background(), []string{
		"run", "suite", suite, "--project-root", root,
		"--report-json", jsonReport,
		"--report-junit", junit,
		"--event-jsonl", events,
	})
	if code != ExitValidationFailed {
		t.Fatalf("exit=%d want %d", code, ExitValidationFailed)
	}
	for _, path := range []string{jsonReport, junit, events} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("validation report %s missing: %v", path, err)
		}
	}
	b, err := os.ReadFile(jsonReport)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"statusCode": -1`) || !strings.Contains(string(b), `"validationFailed": true`) || !strings.Contains(string(b), `"VF1101"`) {
		t.Fatalf("validation JSON report missing stable failure state: %s", b)
	}
	b, err = os.ReadFile(junit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `name="validation"`) || !strings.Contains(string(b), `type="veriflow.validation"`) {
		t.Fatalf("validation JUnit testcase missing: %s", b)
	}
	b, err = os.ReadFile(events)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"event_type":"validation.error"`) || !strings.Contains(string(b), `"VF1101"`) {
		t.Fatalf("validation event JSONL missing: %s", b)
	}
}

func TestProjectConfigAppliesArtifactAndSingleReportPaths(t *testing.T) {
	root := t.TempDir()
	mustWriteCLI(t, filepath.Join(root, "veriflow.yml"), `runtime:
  artifactsRoot: generated-artifacts
reports:
  json: reports/suite.json
  junit: reports/junit.xml
`)
	suite := filepath.Join(root, "suites", "one.yml")
	mustWriteCLI(t, suite, `formatVersion: "1.0"
kind: testSuite
info:
  name: one
tests:
  - id: one
    steps:
      - id: local
        request:
          method: GET
          url: "http://127.0.0.1:1"
        skip: true
`)

	opts := optionSet{flags: map[string]bool{}, values: map[string][]string{}}
	applied, diagnostics := applyProjectConfig(root, opts)
	if hasDiagnosticErrors(diagnostics) {
		t.Fatalf("unexpected config diagnostics: %#v", diagnostics)
	}
	if got := applied.one("--artifacts-root"); got != filepath.Join(root, "generated-artifacts") {
		t.Fatalf("artifacts root=%q", got)
	}
	if got := applied.one("--report-json"); got != filepath.Join(root, "reports", "suite.json") {
		t.Fatalf("single report path=%q", got)
	}
	if got := applied.one("--report-junit"); got != filepath.Join(root, "reports", "junit.xml") {
		t.Fatalf("junit path=%q", got)
	}
}

func TestMalformedVariableFileIsValidationFailureAndReportable(t *testing.T) {
	root := t.TempDir()
	suite := filepath.Join(root, "suites", "one.yml")
	mustWriteCLI(t, suite, `formatVersion: "1.0"
kind: testSuite
info:
  name: one
tests:
  - id: one
    steps:
      - id: wait
        wait:
          forMs: 1
`)
	vars := filepath.Join(root, "vars.yml")
	mustWriteCLI(t, vars, "- one\n- two\n")
	report := filepath.Join(root, "out", "suite.json")
	code := RunContext(context.Background(), []string{
		"run", "suite", suite, "--project-root", root,
		"--var-file", vars, "--report-json", report,
	})
	if code != ExitValidationFailed {
		t.Fatalf("exit=%d want %d", code, ExitValidationFailed)
	}
	b, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"statusCode": -1`) || !strings.Contains(string(b), `"VF1001"`) {
		t.Fatalf("malformed variable file did not produce structured failed report: %s", b)
	}
}

func TestDiscoveredMissingRootWritesRequestedFailureReports(t *testing.T) {
	base := t.TempDir()
	missing := filepath.Join(base, "missing-project")
	aggregate := filepath.Join(base, "out", "run.json")
	junit := filepath.Join(base, "out", "junit.xml")
	events := filepath.Join(base, "out", "events.jsonl")
	code := RunContext(context.Background(), []string{
		"run", "discovered", missing,
		"--report-consolidated-json", aggregate,
		"--report-junit", junit,
		"--event-jsonl", events,
	})
	if code != ExitValidationFailed {
		t.Fatalf("exit=%d want %d", code, ExitValidationFailed)
	}
	for _, path := range []string{aggregate, junit, events} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("missing-root failure report %s missing: %v", path, err)
		}
	}
	b, err := os.ReadFile(aggregate)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"statusCode": -1`) || !strings.Contains(string(b), `"VF1604"`) {
		t.Fatalf("aggregate missing structured project-root failure: %s", b)
	}
	b, err = os.ReadFile(junit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `type="veriflow.validation"`) {
		t.Fatalf("JUnit missing validation failure: %s", b)
	}
	b, err = os.ReadFile(events)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"VF1604"`) || !strings.Contains(string(b), `"event_type":"validation.error"`) {
		t.Fatalf("event JSONL missing project-root failure: %s", b)
	}
}

func TestProjectConfigBooleanDefaultsCanBeExplicitlyDisabled(t *testing.T) {
	root := t.TempDir()
	mustWriteCLI(t, filepath.Join(root, "veriflow.yml"), `ci:
  failIfNoTests: true
network:
  cookieJar: true
  insecureSkipTlsVerify: true
`)
	opts := optionSet{
		flags: map[string]bool{
			"--no-fail-if-no-tests": true,
			"--no-cookie-jar":       true,
			"--verify-tls":          true,
		},
		values: map[string][]string{},
	}
	applied, diagnostics := applyProjectConfig(root, opts)
	if hasDiagnosticErrors(diagnostics) {
		t.Fatalf("unexpected config diagnostics: %#v", diagnostics)
	}
	for _, positive := range []string{"--fail-if-no-tests", "--cookie-jar", "--insecure-skip-tls-verify"} {
		if applied.flags[positive] {
			t.Fatalf("project config unexpectedly overrode explicit negative flag for %s", positive)
		}
	}
}

func TestConflictingBooleanOverridesAreUsageErrors(t *testing.T) {
	root := t.TempDir()
	for _, args := range [][]string{
		{"run", "discovered", root, "--fail-if-no-tests", "--no-fail-if-no-tests"},
		{"run", "discovered", root, "--cookie-jar", "--no-cookie-jar"},
		{"run", "discovered", root, "--insecure-skip-tls-verify", "--verify-tls"},
	} {
		if code := RunContext(context.Background(), args); code != ExitUsageError {
			t.Fatalf("args=%v exit=%d want %d", args, code, ExitUsageError)
		}
	}
}

func TestCIModeNoTestsFinalizesFailureBeforeReporting(t *testing.T) {
	root := t.TempDir()
	suite := filepath.Join(root, "suites", "one.yml")
	mustWriteCLI(t, suite, `formatVersion: "1.0"
kind: testSuite
id: one
info:
  name: one
tests:
  - id: selected
    tags: [smoke]
    steps:
      - id: wait
        wait:
          forMs: 1
`)
	report := filepath.Join(root, "result.json")
	output := captureStdoutCLI(t, func() {
		code := RunContext(context.Background(), []string{"run", "suite", suite, "--project-root", root, "--test-id", "does-not-exist", "--ci", "--report-json", report, "--json"})
		if code != ExitRuntimeFailed {
			t.Fatalf("exit=%d want %d", code, ExitRuntimeFailed)
		}
	})
	var stdoutDoc map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &stdoutDoc); err != nil {
		t.Fatalf("--json must emit exactly one final JSON document, output=%q err=%v", output, err)
	}
	if stdoutDoc["status"] != "failed" || int(stdoutDoc["statusCode"].(float64)) != -1 {
		t.Fatalf("stdout final outcome=%#v", stdoutDoc)
	}
	failure, _ := stdoutDoc["runtimeError"].(map[string]any)
	if failure["code"] != "VF5004" || failure["name"] != "no-tests-selected" {
		t.Fatalf("no-tests failure missing from stdout: %#v", stdoutDoc)
	}

	data, err := os.ReadFile(report)
	if err != nil {
		t.Fatal(err)
	}
	var reportDoc map[string]any
	if err := json.Unmarshal(data, &reportDoc); err != nil {
		t.Fatal(err)
	}
	if reportDoc["status"] != "failed" || int(reportDoc["statusCode"].(float64)) != -1 {
		t.Fatalf("report must agree with process failure: %#v", reportDoc)
	}
}

func TestDiscoveredCIModeNoTestsWritesFailedConsolidatedReport(t *testing.T) {
	root := t.TempDir()
	mustWriteCLI(t, filepath.Join(root, "suites", "one.yml"), `formatVersion: "1.0"
kind: testSuite
id: one
info:
  name: one
tests:
  - id: selected
    tags: [smoke]
    steps:
      - id: wait
        wait:
          forMs: 1
`)
	reportPath := filepath.Join(root, "consolidated.json")
	output := captureStdoutCLI(t, func() {
		code := RunContext(context.Background(), []string{"run", "discovered", root, "--tag", "does-not-exist", "--ci", "--report-consolidated-json", reportPath, "--json"})
		if code != ExitRuntimeFailed {
			t.Fatalf("exit=%d want %d", code, ExitRuntimeFailed)
		}
	})
	var stdoutDoc map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &stdoutDoc); err != nil {
		t.Fatalf("discovered --json must be one JSON document, output=%q err=%v", output, err)
	}
	result, _ := stdoutDoc["result"].(map[string]any)
	failure, _ := stdoutDoc["failure"].(map[string]any)
	if result["status"] != "failed" || int(result["statusCode"].(float64)) != -1 || int(result["totalTests"].(float64)) != 0 {
		t.Fatalf("consolidated stdout outcome=%#v", stdoutDoc)
	}
	if failure["code"] != "VF5004" || failure["name"] != "no-tests-selected" {
		t.Fatalf("consolidated no-tests failure=%#v", stdoutDoc)
	}
	data, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	var reportDoc map[string]any
	if err := json.Unmarshal(data, &reportDoc); err != nil {
		t.Fatal(err)
	}
	reportResult := reportDoc["result"].(map[string]any)
	if reportResult["status"] != "failed" || int(reportResult["statusCode"].(float64)) != -1 {
		t.Fatalf("written consolidated report disagrees with exit status: %#v", reportDoc)
	}
}
