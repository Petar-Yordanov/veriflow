package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Engine struct{ HTTPClient *http.Client }

func New() *Engine { return &Engine{HTTPClient: defaultHTTPClient()} }
func (e *Engine) Discover(projectRoot string) (DiscoveryResult, error) {
	return discoverProject(projectRoot)
}
func (e *Engine) Load(path string) (LoadedDocument, error) { return loadDocument(path) }
func (e *Engine) Validate(suitePath, environmentPath, projectRoot string) (ValidationResult, error) {
	b, err := loadBundle(suitePath, environmentPath, projectRoot)
	if err != nil {
		return ValidationResult{}, err
	}
	return ValidateBundle(b), nil
}

type RunnerOptions struct {
	Seed                  int64
	ProjectRoot           string
	ArtifactsRoot         string
	StopOnValidationError bool
	VariableOverrides     map[string]any
	SensitiveVariables    map[string]bool
	MaxResponseBytes      int64
	SuiteTimeoutMS        int
	TestTimeoutMS         int
	CleanupTimeoutMS      int
	TestIDs               map[string]bool
	TestNames             map[string]bool
	Tags                  map[string]bool
	ExcludeTestIDs        map[string]bool
	ExcludeTestNames      map[string]bool
	ExcludeTags           map[string]bool
}

func DefaultRunnerOptions() RunnerOptions {
	return RunnerOptions{Seed: 42, StopOnValidationError: true, VariableOverrides: map[string]any{}, SensitiveVariables: map[string]bool{}, MaxResponseBytes: DefaultMaxResponseBytes, CleanupTimeoutMS: 15000}
}
func (o RunnerOptions) testMatches(t TestSpec) bool {
	if o.ExcludeTestIDs[t.ID] || (t.BaseID != "" && o.ExcludeTestIDs[t.BaseID]) || o.ExcludeTestNames[t.Name] {
		return false
	}
	for _, tag := range t.Tags {
		if o.ExcludeTags[tag] {
			return false
		}
	}
	active := len(o.TestIDs) > 0 || len(o.TestNames) > 0 || len(o.Tags) > 0
	if !active {
		return true
	}
	if o.TestIDs[t.ID] || (t.BaseID != "" && o.TestIDs[t.BaseID]) {
		return true
	}
	if o.TestNames[t.Name] {
		return true
	}
	for _, tag := range t.Tags {
		if o.Tags[tag] {
			return true
		}
	}
	return false
}

func (e *Engine) Run(ctx context.Context, suitePath, environmentPath string, reporter Reporter, options RunnerOptions) (SuiteResult, error) {
	if reporter == nil {
		reporter = &SummaryReporter{}
	}
	if options.Seed == 0 {
		options.Seed = 42
	}
	if options.ProjectRoot == "" {
		inferred, inferErr := inferProjectRoot(suitePath)
		if inferErr != nil {
			return SuiteResult{}, userLoadError("load-error", suitePath, "cannot infer project root: %v", inferErr)
		}
		options.ProjectRoot = inferred
	}
	b, err := loadBundle(suitePath, environmentPath, options.ProjectRoot)
	if err != nil {
		return SuiteResult{}, err
	}
	validation := ValidateBundle(b)
	redactor := NewRedactor()
	for name, sensitive := range options.SensitiveVariables {
		if sensitive {
			if v, ok := lookupRawPath(options.VariableOverrides, name); ok {
				redactor.Add(v)
			}
		}
	}
	runID := newUUID()
	var reporterErr error
	emit := func(kind string, p map[string]any) error {
		if reporterErr != nil {
			return reporterErr
		}
		if err := reporter.OnEvent(redactor.Event(NewEvent(kind, p))); err != nil {
			reporterErr = runtimeNamedError("reporter-error", fmt.Errorf("reporter event %s failed: %w", kind, err))
			return reporterErr
		}
		return nil
	}
	// Event sinks are observational: failures are captured in reporterErr and
	// returned after execution/finalization, but they do not change test execution
	// or prevent teardown. This preserves the real test result while still making
	// the CLI invocation fail when requested reporting is broken.
	for _, d := range validation.Diagnostics {
		if d.IsError() {
			_ = emit("validation.error", map[string]any{"code": d.Code, "name": d.Name, "message": d.Message, "file": d.Location.File, "path": d.Location.DocumentPath, "line": d.Location.Line, "column": d.Location.Column})
		}
	}
	if options.StopOnValidationError && !validation.OK() {
		name := filepath.Base(suitePath)
		if typed, ok := b.Suite.Typed.(TestSuiteSpec); ok {
			if typed.Info.Name != "" {
				name = typed.Info.Name
			} else if typed.Metadata.ID != "" {
				name = typed.Metadata.ID
			}
		}
		res := ValidationFailureResult(name, validation.Diagnostics)
		res = redactor.SuiteResult(res)
		if err := reporter.Finalize(res); err != nil && reporterErr == nil {
			reporterErr = runtimeNamedError("reporter-error", fmt.Errorf("reporter finalize failed: %w", err))
		}
		if reporterErr != nil {
			return res, reporterErr
		}
		return res, nil
	}

	suite := b.Suite.Typed.(TestSuiteSpec)
	suiteCtx := ctx
	suiteCancel := func() {}
	suiteTimeoutMS := suite.TimeoutMS
	if suiteTimeoutMS == 0 {
		suiteTimeoutMS = options.SuiteTimeoutMS
	}
	if suiteTimeoutMS > 0 {
		suiteCtx, suiteCancel = context.WithTimeout(ctx, time.Duration(suiteTimeoutMS)*time.Millisecond)
	}
	defer suiteCancel()
	started := time.Now()
	startedAt := started.UTC().Format(time.RFC3339Nano)
	suiteRuntime := map[string]any{}
	results := []TestResult{}
	beforeAll := []StepResult{}
	afterAll := []StepResult{}

	selectedTests := make([]TestSpec, 0, len(suite.Tests))
	hasRunnableTest := false
	for _, baseTest := range suite.Tests {
		for _, test := range ExpandTestCases(baseTest) {
			if options.testMatches(test) {
				selectedTests = append(selectedTests, test)
				if !test.Skip {
					hasRunnableTest = true
				}
			}
		}
	}

	_ = emit("suite.started", map[string]any{"name": suite.Info.Name, "file": b.Suite.Path})
	beforeAllOK := true
	if hasRunnableTest && len(suite.Hooks.BeforeAll) > 0 {
		hookTest := TestSpec{ID: "__beforeAll__", Name: "beforeAll"}
		beforeAll, beforeAllOK, err = e.runHookSteps(suiteCtx, b, suite, suiteRuntime, map[string]any{}, hookTest, "beforeAll", suite.Hooks.BeforeAll, emit, options, redactor, runID)
		if err != nil {
			return SuiteResult{}, err
		}
	}

	suiteError := ""
	var suiteRuntimeError *RuntimeDiagnostic
	if beforeAllOK {
		for _, test := range selectedTests {
			if err := suiteCtx.Err(); err != nil {
				suiteError = fmt.Sprintf("suite execution interrupted: %v", err)
				d := RuntimeDiagnosticFromError(runtimeNamedError("execution-cancelled", err))
				suiteRuntimeError = &d
				break
			}
			tr, runErr := e.runTest(suiteCtx, b, suite, suiteRuntime, test, emit, options, redactor, runID)
			if runErr != nil {
				return SuiteResult{}, runErr
			}
			results = append(results, tr)
			if suiteCtx.Err() != nil {
				suiteError = fmt.Sprintf("suite execution interrupted: %v", suiteCtx.Err())
				d := RuntimeDiagnosticFromError(runtimeNamedError("execution-cancelled", suiteCtx.Err()))
				suiteRuntimeError = &d
				break
			}
		}
	} else if suiteCtx.Err() != nil {
		suiteError = fmt.Sprintf("suite setup interrupted: %v", suiteCtx.Err())
		d := RuntimeDiagnosticFromError(runtimeNamedError("execution-cancelled", suiteCtx.Err()))
		suiteRuntimeError = &d
	}

	afterAllOK := true
	if hasRunnableTest && len(suite.Hooks.AfterAll) > 0 {
		cleanupCtx, cleanupCancel := cleanupContext(suiteCtx, options.CleanupTimeoutMS)
		hookTest := TestSpec{ID: "__afterAll__", Name: "afterAll"}
		afterAll, afterAllOK, err = e.runHookSteps(cleanupCtx, b, suite, suiteRuntime, map[string]any{}, hookTest, "afterAll", suite.Hooks.AfterAll, emit, options, redactor, runID)
		cleanupCancel()
		if err != nil {
			return SuiteResult{}, err
		}
	}

	passed, failed, skipped := 0, 0, 0
	for _, t := range results {
		switch t.Status {
		case Passed:
			passed++
		case Failed:
			failed++
		case Skipped:
			skipped++
		}
	}
	status := Passed
	if !beforeAllOK || !afterAllOK || failed > 0 || suiteError != "" {
		status = Failed
	} else if len(results) > 0 && skipped == len(results) {
		status = Skipped
	}
	res := SuiteResult{
		SchemaVersion: ReportSchemaVersion, EngineVersion: EngineVersion, Name: suite.Info.Name,
		Status: status, StatusCode: logicalStatusCode(status), StartedAt: startedAt, FinishedAt: time.Now().UTC().Format(time.RFC3339Nano),
		DurationMS: float64(time.Since(started).Microseconds()) / 1000, Error: redactor.String(suiteError), RuntimeError: suiteRuntimeError,
		PassedCount: passed, FailedCount: failed, SkippedCount: skipped,
		BeforeAll: beforeAll, Tests: results, AfterAll: afterAll, Diagnostics: diagMaps(validation.Diagnostics),
	}
	_ = emit("suite.finished", map[string]any{
		"status": status, "passedCount": passed, "failedCount": failed, "skippedCount": skipped,
		"beforeAllPassed": beforeAllOK, "afterAllPassed": afterAllOK,
	})
	res = redactor.SuiteResult(res)
	if err := reporter.Finalize(res); err != nil && reporterErr == nil {
		reporterErr = runtimeNamedError("reporter-error", fmt.Errorf("reporter finalize failed: %w", err))
	}
	if reporterErr != nil {
		return res, reporterErr
	}
	return res, nil
}

func (e *Engine) runTest(ctx context.Context, b LoadedBundle, suite TestSuiteSpec, suiteRuntime map[string]any, test TestSpec, emit func(string, map[string]any) error, o RunnerOptions, redactor *Redactor, runID string) (TestResult, error) {
	started := time.Now()
	startedAt := started.UTC().Format(time.RFC3339Nano)
	_ = emit("test.started", map[string]any{"id": test.ID, "baseId": test.BaseID, "case": test.CaseID, "name": test.Name})
	if test.Skip {
		r := TestResult{ID: test.ID, BaseID: test.BaseID, Case: test.CaseID, Name: test.Name, Status: Skipped, StartedAt: startedAt, FinishedAt: time.Now().UTC().Format(time.RFC3339Nano), Tags: test.Tags}
		_ = emit("test.finished", map[string]any{"id": test.ID, "name": test.Name, "status": Skipped})
		return r, nil
	}

	testCtx := ctx
	cancel := func() {}
	timeoutMS := test.TimeoutMS
	if timeoutMS == 0 {
		timeoutMS = o.TestTimeoutMS
	}
	if timeoutMS > 0 {
		testCtx, cancel = context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	}
	defer cancel()

	testRuntime := map[string]any{}
	beforeEach := []StepResult{}
	afterEach := []StepResult{}
	steps := []StepResult{}
	status := Passed
	testError := ""
	var testRuntimeError *RuntimeDiagnostic

	beforeEachOK := true
	if len(suite.Hooks.BeforeEach) > 0 {
		var err error
		beforeEach, beforeEachOK, err = e.runHookSteps(testCtx, b, suite, suiteRuntime, testRuntime, test, "beforeEach", suite.Hooks.BeforeEach, emit, o, redactor, runID)
		if err != nil {
			return TestResult{}, err
		}
		if !beforeEachOK {
			status = Failed
		}
	}

	if beforeEachOK {
		for _, step := range test.Steps {
			if err := testCtx.Err(); err != nil {
				status = Failed
				testError = fmt.Sprintf("test execution interrupted: %v", err)
				d := RuntimeDiagnosticFromError(runtimeNamedError("execution-cancelled", err))
				testRuntimeError = &d
				break
			}
			sr := e.runStep(testCtx, b, suite, suiteRuntime, testRuntime, test, step, emit, o, redactor, runID)
			steps = append(steps, sr)
			if sr.Status == Failed {
				status = Failed
				if testCtx.Err() != nil {
					testError = fmt.Sprintf("test execution interrupted: %v", testCtx.Err())
					d := RuntimeDiagnosticFromError(runtimeNamedError("execution-cancelled", testCtx.Err()))
					testRuntimeError = &d
				}
				if !step.ContinueOnFailure {
					break
				}
			}
		}
	}

	// Teardown is attempted even after the test context is cancelled. It receives
	// a separate bounded context so Ctrl+C / suite or test timeouts cannot skip
	// cleanup forever or let cleanup run without a deadline.
	if len(suite.Hooks.AfterEach) > 0 {
		cleanupCtx, cleanupCancel := cleanupContext(testCtx, o.CleanupTimeoutMS)
		var err error
		var afterEachOK bool
		afterEach, afterEachOK, err = e.runHookSteps(cleanupCtx, b, suite, suiteRuntime, testRuntime, test, "afterEach", suite.Hooks.AfterEach, emit, o, redactor, runID)
		cleanupCancel()
		if err != nil {
			return TestResult{}, err
		}
		if !afterEachOK {
			status = Failed
		}
	}

	if testCtx.Err() != nil && testError == "" {
		status = Failed
		testError = fmt.Sprintf("test execution interrupted: %v", testCtx.Err())
		d := RuntimeDiagnosticFromError(runtimeNamedError("execution-cancelled", testCtx.Err()))
		testRuntimeError = &d
	}
	if status != Failed && len(steps) > 0 {
		allSkipped := true
		for _, st := range steps {
			if st.Status != Skipped {
				allSkipped = false
				break
			}
		}
		if allSkipped {
			status = Skipped
		}
	}
	r := TestResult{
		ID: test.ID, BaseID: test.BaseID, Case: test.CaseID, Name: test.Name, Status: status, StartedAt: startedAt,
		FinishedAt: time.Now().UTC().Format(time.RFC3339Nano), DurationMS: float64(time.Since(started).Microseconds()) / 1000,
		Error: redactor.String(testError), RuntimeError: testRuntimeError, Tags: test.Tags, BeforeEach: beforeEach, Steps: steps, AfterEach: afterEach,
	}
	_ = emit("test.finished", map[string]any{"id": test.ID, "name": test.Name, "status": status, "error": testError})
	return r, nil
}

func (e *Engine) runHookSteps(ctx context.Context, b LoadedBundle, suite TestSuiteSpec, suiteRuntime, testRuntime map[string]any, test TestSpec, hookName string, steps []StepSpec, emit func(string, map[string]any) error, o RunnerOptions, redactor *Redactor, runID string) ([]StepResult, bool, error) {
	_ = emit("hook.started", map[string]any{"hook": hookName, "testId": test.ID, "testName": test.Name})
	results := make([]StepResult, 0, len(steps))
	ok := true
	for _, step := range steps {
		if err := ctx.Err(); err != nil {
			now := time.Now().UTC().Format(time.RFC3339Nano)
			r := stepFailure(step, now, runtimeNamedError("execution-cancelled", err))
			r.FinishedAt = now
			results = append(results, redactor.StepResult(r))
			ok = false
			break
		}
		sr := e.runStep(ctx, b, suite, suiteRuntime, testRuntime, test, step, emit, o, redactor, runID)
		results = append(results, sr)
		if sr.Status == Failed {
			ok = false
			if !step.ContinueOnFailure {
				break
			}
		}
	}
	_ = emit("hook.finished", map[string]any{"hook": hookName, "testId": test.ID, "testName": test.Name, "passed": ok})
	return results, ok, nil
}

func (e *Engine) runStep(ctx context.Context, b LoadedBundle, suite TestSuiteSpec, suiteRuntime, testRuntime map[string]any, test TestSpec, step StepSpec, emit func(string, map[string]any) error, o RunnerOptions, redactor *Redactor, runID string) StepResult {
	started := time.Now()
	startedAt := started.UTC().Format(time.RFC3339Nano)
	if step.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(step.TimeoutMS)*time.Millisecond)
		defer cancel()
	}
	_ = emit("step.started", map[string]any{"id": step.ID, "name": step.Name})
	finish := func(r StepResult) StepResult {
		r.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		r.DurationMS = float64(time.Since(started).Microseconds()) / 1000
		r = redactor.StepResult(r)
		_ = emit("step.finished", map[string]any{"id": step.ID, "name": step.Name, "status": r.Status})
		return r
	}
	if step.Skip {
		return finish(StepResult{ID: step.ID, Name: step.Name, Status: Skipped, StartedAt: startedAt})
	}
	if step.Wait != nil && step.Wait.BeforeMS > 0 {
		if err := sleepContext(ctx, time.Duration(step.Wait.BeforeMS)*time.Millisecond); err != nil {
			return finish(stepFailure(step, startedAt, runtimeNamedError("execution-cancelled", err)))
		}
	}
	if step.Wait != nil && step.Wait.ForMS > 0 && step.Use == "" && step.Request == nil {
		if err := sleepContext(ctx, time.Duration(step.Wait.ForMS)*time.Millisecond); err != nil {
			return finish(stepFailure(step, startedAt, runtimeNamedError("execution-cancelled", err)))
		}
		return finish(StepResult{ID: step.ID, Name: step.Name, Status: Passed, StartedAt: startedAt})
	}

	requestDef, req, err := resolveStepRequest(b, step)
	if err != nil {
		return e.runtimeFailure(step, startedAt, runtimeNamedError("request-reference-error", err), emit, finish)
	}
	req = applySuiteDefaults(req, suite.Defaults)
	req, err = applyMutations(req, step)
	if err != nil {
		return e.runtimeFailure(step, startedAt, runtimeNamedError("request-build-error", err), emit, finish)
	}
	effectiveRetry := step.Retry
	if effectiveRetry == nil {
		effectiveRetry = suite.Defaults.Retry
	}

	envVars := map[string]any{}
	envName := ""
	if b.Environment != nil {
		env := b.Environment.Typed.(EnvironmentSpec)
		envVars = cloneMap(env.Variables)
		envName = env.Name
	}
	envVars = deepMerge(envVars, o.VariableOverrides)

	inputs := cloneMap(step.With)
	if requestDef != nil {
		for name, def := range requestDef.Inputs {
			if _, exists := inputs[name]; !exists && def.Default != nil {
				inputs[name] = deepCopy(def.Default)
			}
		}
	}

	prepareForIteration := func(iterationIndex int) (PreparedRequest, map[string]any, error) {
		layers := VariableLayers{
			Environment: envVars, SuiteDeclared: suite.Globals.Variables, SuiteRuntime: suiteRuntime,
			TestDeclared: test.Variables, TestRuntime: testRuntime, StepDeclared: step.Variables,
			Inputs: inputs,
			Builtins: BuildBuiltinVariables(DeriveBuiltinSeed(o.Seed, suite.Metadata.ID, suite.Info.Name, test.ID, step.ID, iterationIndex), map[string]any{
				"runId": runID, "suiteId": suite.Metadata.ID, "suiteName": suite.Info.Name,
				"testId": test.ID, "testName": test.Name, "stepId": step.ID, "stepName": step.Name,
				"environmentName": envName, "iterationIndex": iterationIndex,
			}),
		}
		lookup, resolveErr := ResolveVariables(layers.AsLookup())
		if resolveErr != nil {
			return PreparedRequest{}, nil, resolveErr
		}
		if requestDef != nil {
			resolvedInputs := asMap(lookup["inputs"])
			if inputErr := ValidateRequestInputs(*requestDef, resolvedInputs); inputErr != nil {
				return PreparedRequest{}, nil, runtimeNamedError("input-contract-error", inputErr)
			}
			for name, def := range requestDef.Inputs {
				if def.Sensitive {
					if v, ok := resolvedInputs[name]; ok {
						redactor.Add(v)
					}
				}
			}
		}
		prepared, prepErr := PrepareRequest(req, lookup, b.ProjectRoot)
		if prepErr != nil {
			return PreparedRequest{}, nil, runtimeNamedError("request-build-error", prepErr)
		}
		return prepared, lookup, nil
	}

	warmup, count := 0, 1
	if step.Repeat != nil {
		warmup = step.Repeat.WarmupCount
		count = step.Repeat.Count
		if count <= 0 {
			count = 1
		}
	}
	timeout := step.TimeoutMS
	if timeout == 0 {
		timeout = req.TimeoutMS
	}

	for i := 0; i < warmup; i++ {
		prepared, _, prepErr := prepareForIteration(-(warmup - i))
		if prepErr != nil {
			return e.runtimeFailure(step, startedAt, runtimeNamedError("request-build-error", prepErr), emit, finish)
		}
		_, sendErr := SendHTTPWithOptions(ctx, e.HTTPClient, prepared, SendHTTPOptions{TimeoutMS: timeout, FollowRedirects: req.FollowRedirects, MaxResponseBytes: o.MaxResponseBytes})
		if sendErr != nil {
			return e.runtimeFailure(step, startedAt, runtimeHTTPError(sendErr), emit, finish)
		}
	}

	timings := []float64{}
	var response HTTPResponse
	var finalPrepared PreparedRequest
	var finalLookup map[string]any
	retrySummary := RetrySummary{}
	assertions := AssertionOutcome{Passed: true}
	responseExpectation := expectationWithoutPerformance(step.Expect)
	for iteration := 0; iteration < count; iteration++ {
		prepared, lookup, prepErr := prepareForIteration(iteration)
		if prepErr != nil {
			return e.runtimeFailure(step, startedAt, runtimeNamedError("request-build-error", prepErr), emit, finish)
		}
		finalPrepared, finalLookup = prepared, lookup
		for _, sensitiveValue := range prepared.SensitiveValues {
			redactor.Add(sensitiveValue)
		}
		_ = emit("request.prepared", map[string]any{"stepId": step.ID, "method": prepared.Method, "url": prepared.URL, "iterationIndex": iteration})
		if step.Log != nil {
			payload := map[string]any{"stepId": step.ID, "iterationIndex": iteration}
			if boolEnabled(step.Log.Request.Headers) {
				payload["headers"] = prepared.Headers
			}
			if boolEnabled(step.Log.Request.Body) {
				payload["body"] = string(prepared.Content)
			}
			if len(payload) > 2 {
				_ = emit("request.log", payload)
			}
		}

		attempts := 1
		if effectiveRetry != nil {
			attempts += effectiveRetry.Count
		}
		iterationElapsed := 0.0
		for attempt := 1; attempt <= attempts; attempt++ {
			call := time.Now()
			response, err = SendHTTPWithOptions(ctx, e.HTTPClient, prepared, SendHTTPOptions{TimeoutMS: timeout, FollowRedirects: req.FollowRedirects, MaxResponseBytes: o.MaxResponseBytes})
			elapsed := float64(time.Since(call).Microseconds()) / 1000
			retrySummary.Attempts++
			if err != nil {
				retryable := effectiveRetry != nil && attempt < attempts && ((IsTimeoutError(err) && effectiveRetry.When.Timeouts) || (!IsTimeoutError(err) && effectiveRetry.When.NetworkErrors))
				if !retryable {
					return e.runtimeFailure(step, startedAt, runtimeHTTPError(err), emit, finish)
				}
				retrySummary.Retried = true
				reason := "network error"
				if IsTimeoutError(err) {
					reason = "timeout"
				}
				retrySummary.Reasons = append(retrySummary.Reasons, reason)
				if sleepErr := sleepContext(ctx, retryDelay(effectiveRetry, attempt)); sleepErr != nil {
					return e.runtimeFailure(step, startedAt, runtimeNamedError("execution-cancelled", sleepErr), emit, finish)
				}
				continue
			}
			retryable := effectiveRetry != nil && containsInt(effectiveRetry.When.StatusIn, response.StatusCode) && attempt < attempts
			if retryable {
				retrySummary.Retried = true
				retrySummary.Reasons = append(retrySummary.Reasons, fmt.Sprintf("status %d", response.StatusCode))
				if sleepErr := sleepContext(ctx, retryDelay(effectiveRetry, attempt)); sleepErr != nil {
					return e.runtimeFailure(step, startedAt, runtimeNamedError("execution-cancelled", sleepErr), emit, finish)
				}
				continue
			}
			iterationElapsed = elapsed
			timings = append(timings, elapsed)
			break
		}

		// Discover extraction-defined sensitive values before response logging for
		// every measured iteration. Extraction scope mutation remains final-response
		// only, but secrets must never leak from an earlier repeated response.
		if preview, previewErr := ExtractValues(step.Extract, requestDef, response, lookup); previewErr == nil {
			for name, value := range preview.Variables {
				if effectiveExtractionSpec(step.Extract[name], requestDef).Sensitive {
					redactor.Add(value)
				}
			}
		}
		_ = emit("response.received", map[string]any{"stepId": step.ID, "iterationIndex": iteration, "statusCode": response.StatusCode, "totalMs": iterationElapsed})
		if step.Log != nil {
			payload := map[string]any{"stepId": step.ID, "iterationIndex": iteration, "statusCode": response.StatusCode}
			if boolEnabled(step.Log.Response.Headers) {
				payload["headers"] = response.Headers
			}
			if boolEnabled(step.Log.Response.Body) {
				payload["body"] = string(response.Body)
			}
			if len(payload) > 3 {
				_ = emit("response.log", payload)
			}
		}

		iterationAssertions, assertionErr := EvaluateAssertions(responseExpectation, response, nil, response.JSON, lookup)
		if assertionErr != nil {
			return e.runtimeFailure(step, startedAt, runtimeNamedError("assertion-evaluation-error", assertionErr), emit, finish)
		}
		if count > 1 {
			prefixAssertionTargets(iterationAssertions.Evaluations, iteration)
		}
		assertions.Evaluations = append(assertions.Evaluations, iterationAssertions.Evaluations...)
		assertions.Passed = assertions.Passed && iterationAssertions.Passed
	}

	timing := timingSummary(timings)
	performanceAssertions, err := EvaluateAssertions(performanceOnlyExpectation(step.Expect), response, timing, response.JSON, finalLookup)
	if err != nil {
		return e.runtimeFailure(step, startedAt, runtimeNamedError("assertion-evaluation-error", err), emit, finish)
	}
	assertions.Evaluations = append(assertions.Evaluations, performanceAssertions.Evaluations...)
	assertions.Passed = assertions.Passed && performanceAssertions.Passed
	_ = emit("assertions.evaluated", map[string]any{"stepId": step.ID, "passed": assertions.Passed, "count": len(assertions.Evaluations), "iterations": count})

	// Extraction is intentionally performed from the final measured response.
	// Repeated assertions validate every measured response; extracted state has a
	// single deterministic source for later steps.
	extraction, err := ExtractValues(step.Extract, requestDef, response, finalLookup)
	if err != nil {
		return e.runtimeFailure(step, startedAt, runtimeNamedError("extraction-error", err), emit, finish)
	}
	for name, value := range extraction.Variables {
		if effectiveExtractionSpec(step.Extract[name], requestDef).Sensitive {
			redactor.Add(value)
		}
	}
	names := []string{}
	for name, value := range extraction.Variables {
		names = append(names, name)
		spec := effectiveExtractionSpec(step.Extract[name], requestDef)
		switch spec.Scope {
		case SuiteScope:
			suiteRuntime[name] = value
		case TestScope:
			testRuntime[name] = value
		}
	}
	sort.Strings(names)
	_ = emit("extraction.completed", map[string]any{"stepId": step.ID, "names": names, "passed": extraction.OK})
	artifacts, err := persistArtifacts(step, response, timing, o.ArtifactsRoot, b.ProjectRoot, emit, redactor)
	if err != nil {
		return e.runtimeFailure(step, startedAt, runtimeNamedError("filesystem-error", err), emit, finish)
	}
	if step.Wait != nil && step.Wait.AfterMS > 0 {
		if err := sleepContext(ctx, time.Duration(step.Wait.AfterMS)*time.Millisecond); err != nil {
			return e.runtimeFailure(step, startedAt, runtimeNamedError("execution-cancelled", err), emit, finish)
		}
	}
	status := Passed
	if !assertions.Passed || !extraction.OK {
		status = Failed
	}
	total, avg, p95, max := timing["totalMs"], timing["avgMs"], timing["p95Ms"], timing["maxMs"]
	return finish(StepResult{ID: step.ID, Name: step.Name, Status: status, StartedAt: startedAt, RequestSummary: &RequestSummary{Method: finalPrepared.Method, URL: finalPrepared.URL, Headers: anyHeaders(finalPrepared.Headers)}, ResponseSummary: &ResponseSummary{StatusCode: response.StatusCode, Headers: response.Headers, BodyPreview: truncate(string(response.Body), 500)}, Assertions: assertions.Evaluations, Extractions: extraction.Results, RetrySummary: retrySummary, Timing: TimingMetrics{TotalMS: &total, AvgMS: &avg, P95MS: &p95, MaxMS: &max}, Artifacts: artifacts})
}

func expectationWithoutPerformance(expect *ExpectationSpec) *ExpectationSpec {
	if expect == nil {
		return nil
	}
	copy := *expect
	copy.Performance = nil
	return &copy
}

func performanceOnlyExpectation(expect *ExpectationSpec) *ExpectationSpec {
	if expect == nil || expect.Performance == nil {
		return nil
	}
	return &ExpectationSpec{Performance: expect.Performance}
}

func prefixAssertionTargets(evaluations []AssertionEvaluation, iteration int) {
	prefix := fmt.Sprintf("iteration[%d].", iteration)
	for i := range evaluations {
		evaluations[i].Target = prefix + evaluations[i].Target
	}
}

func retryDelay(r *RetrySpec, attempt int) time.Duration {
	if r == nil || r.DelayMS <= 0 {
		return 0
	}
	m := r.BackoffMultiplier
	if m <= 0 {
		m = 1
	}
	delay := float64(r.DelayMS)
	for i := 1; i < attempt; i++ {
		delay *= m
	}
	if r.MaxDelayMS > 0 && delay > float64(r.MaxDelayMS) {
		delay = float64(r.MaxDelayMS)
	}
	return time.Duration(delay) * time.Millisecond
}

func applySuiteDefaults(req RequestSpec, defaults SuiteDefaults) RequestSpec {
	if req.TimeoutMS == 0 {
		req.TimeoutMS = defaults.TimeoutMS
	}
	if req.FollowRedirects == nil && defaults.FollowRedirects != nil {
		b := *defaults.FollowRedirects
		req.FollowRedirects = &b
	}
	if len(defaults.Headers) > 0 {
		h := cloneMap(defaults.Headers)
		for k, v := range req.Headers {
			h[k] = deepCopy(v)
		}
		req.Headers = h
	}
	return req
}

func (e *Engine) runtimeFailure(step StepSpec, startedAt string, err error, emit func(string, map[string]any) error, finish func(StepResult) StepResult) StepResult {
	diagnostic := RuntimeDiagnosticFromError(err)
	_ = emit("runtime.error", map[string]any{"stepId": step.ID, "code": diagnostic.Code, "name": diagnostic.Name, "message": diagnostic.Message})
	return finish(stepFailure(step, startedAt, err))
}
func stepFailure(step StepSpec, startedAt string, err error) StepResult {
	diagnostic := RuntimeDiagnosticFromError(err)
	return StepResult{ID: step.ID, Name: step.Name, Status: Failed, StartedAt: startedAt, Error: err.Error(), RuntimeError: &diagnostic}
}
func resolveStepRequest(b LoadedBundle, step StepSpec) (*RequestDefinitionSpec, RequestSpec, error) {
	if step.Use == "" {
		if step.Request == nil {
			return nil, RequestSpec{}, fmt.Errorf("step has no request")
		}
		return nil, *step.Request, nil
	}
	p, err := ResolveRequestReference(b.ProjectRoot, step.Use)
	if err != nil {
		return nil, RequestSpec{}, err
	}
	doc, ok := b.ReferencedRequests[p]
	if !ok {
		doc, err = loadDocument(p)
		if err != nil {
			return nil, RequestSpec{}, err
		}
	}
	rd, ok := doc.Typed.(RequestDefinitionSpec)
	if !ok {
		return nil, RequestSpec{}, fmt.Errorf("%s is not a requestDefinition", step.Use)
	}
	return &rd, rd.Request, nil
}
func applyMutations(req RequestSpec, step StepSpec) (RequestSpec, error) {
	if len(step.Extend) == 0 && len(step.Overrides) == 0 && step.TimeoutMS == 0 {
		return req, nil
	}
	raw := requestToMap(req)
	raw = deepMerge(raw, step.Extend)
	for k, v := range step.Overrides {
		raw[k] = deepCopy(v)
	}
	r, err := parseRequest(raw)
	if err != nil {
		return RequestSpec{}, fmt.Errorf("invalid request after extend/overrides: %w", err)
	}
	if step.TimeoutMS > 0 {
		r.TimeoutMS = step.TimeoutMS
	}
	return r, nil
}
func requestToMap(r RequestSpec) map[string]any {
	return map[string]any{"method": r.Method, "url": r.URL, "baseUrl": r.BaseURL, "path": r.Path, "pathParams": r.PathParams, "query": r.Query, "headers": r.Headers, "cookies": r.Cookies, "auth": authToMap(r.Auth), "body": r.Body, "bodyRaw": r.BodyRaw, "bodyFile": r.BodyFile, "bodyFileMode": r.BodyFileMode, "form": r.Form, "multipart": r.Multipart, "timeoutMs": r.TimeoutMS, "followRedirects": func() any {
		if r.FollowRedirects == nil {
			return nil
		}
		return *r.FollowRedirects
	}()}
}
func deepMerge(left, right map[string]any) map[string]any {
	out := cloneMap(left)
	for k, v := range right {
		if lm, ok := out[k].(map[string]any); ok {
			if rm, ok2 := v.(map[string]any); ok2 {
				out[k] = deepMerge(lm, rm)
				continue
			}
		}
		out[k] = deepCopy(v)
	}
	return out
}
func timingSummary(ts []float64) map[string]float64 {
	if len(ts) == 0 {
		return map[string]float64{"totalMs": 0, "avgMs": 0, "p95Ms": 0, "maxMs": 0}
	}
	s := append([]float64{}, ts...)
	sort.Float64s(s)
	sum := 0.0
	for _, v := range s {
		sum += v
	}
	idx := int(float64(len(s)-1)*0.95 + 0.5)
	if idx >= len(s) {
		idx = len(s) - 1
	}
	return map[string]float64{"totalMs": sum, "avgMs": sum / float64(len(s)), "p95Ms": s[idx], "maxMs": s[len(s)-1]}
}
func persistArtifacts(step StepSpec, response HTTPResponse, timing map[string]float64, configuredRoot, projectRoot string, emit func(string, map[string]any) error, redactor *Redactor) ([]string, error) {
	if step.Artifacts == nil {
		return nil, nil
	}
	root := configuredRoot
	if root == "" {
		root = filepath.Join(projectRoot, "artifacts")
	}
	paths := []string{}
	save := func(rel, kind string, data []byte) error {
		if rel == "" {
			return nil
		}
		p, err := saveArtifact(root, rel, data)
		if err != nil {
			return err
		}
		paths = append(paths, p)
		_ = emit("artifact.saved", map[string]any{"stepId": step.ID, "path": p, "kind": kind})
		return nil
	}
	if err := save(step.Artifacts.SaveResponseBodyTo, "responseBody", []byte(redactor.String(string(response.Body)))); err != nil {
		return nil, err
	}
	if step.Artifacts.SaveParsedJSONTo != "" && response.JSON != nil {
		b, err := json.MarshalIndent(redactor.Any(response.JSON), "", "  ")
		if err != nil {
			return nil, fmt.Errorf("serialize parsed JSON artifact: %w", err)
		}
		if err := save(step.Artifacts.SaveParsedJSONTo, "parsedJson", b); err != nil {
			return nil, err
		}
	}
	if step.Artifacts.SaveHeadersTo != "" {
		b, err := json.MarshalIndent(redactor.Any(response.Headers), "", "  ")
		if err != nil {
			return nil, fmt.Errorf("serialize headers artifact: %w", err)
		}
		if err := save(step.Artifacts.SaveHeadersTo, "headers", b); err != nil {
			return nil, err
		}
	}
	if step.Artifacts.SaveTimingTo != "" {
		b, err := json.MarshalIndent(timing, "", "  ")
		if err != nil {
			return nil, fmt.Errorf("serialize timing artifact: %w", err)
		}
		if err := save(step.Artifacts.SaveTimingTo, "timing", b); err != nil {
			return nil, err
		}
	}
	return paths, nil
}
func boolEnabled(v *bool) bool { return v != nil && *v }

func logicalStatusCode(status ResultStatus) int {
	if status == Failed {
		return -1
	}
	return 0
}

func cleanupContext(parent context.Context, timeoutMS int) (context.Context, context.CancelFunc) {
	if timeoutMS <= 0 {
		timeoutMS = 15000
	}
	// Preserve context values but deliberately detach cancellation/deadline.
	base := context.WithoutCancel(parent)
	return context.WithTimeout(base, time.Duration(timeoutMS)*time.Millisecond)
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
func anyHeaders(h map[string]string) map[string]any {
	m := map[string]any{}
	for k, v := range h {
		m[k] = v
	}
	return m
}
func diagMaps(ds []Diagnostic) []map[string]any {
	out := make([]map[string]any, 0, len(ds))
	for _, d := range ds {
		location := map[string]any{}
		if d.Location.File != "" {
			location["file"] = d.Location.File
		}
		if d.Location.DocumentPath != "" {
			location["path"] = d.Location.DocumentPath
		}
		if d.Location.Line != 0 {
			location["line"] = d.Location.Line
		}
		if d.Location.Column != 0 {
			location["column"] = d.Location.Column
		}
		m := map[string]any{"code": d.Code, "message": d.Message, "severity": d.Severity, "location": location}
		if d.Name != "" {
			m["name"] = d.Name
		}
		if len(d.Details) > 0 {
			m["details"] = deepCopy(d.Details)
		}
		out = append(out, m)
	}
	return out
}

func ParseVariableFile(path string) (map[string]any, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, userLoadError("load-error", path, "cannot resolve variable file: %v", err)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, userLoadError("load-error", abs, "cannot read variable file: %v", err)
	}
	m, _, err := parseYAML(data)
	if err != nil {
		return nil, classifyYAMLLoadError(abs, err)
	}
	if asString(m["kind"]) == string(EnvironmentKind) {
		return cloneMap(asMap(m["variables"])), nil
	}
	return m, nil
}
func ParseAdHocVariables(items []string) (map[string]any, error) {
	merged := map[string]any{}
	for _, item := range items {
		idx := strings.IndexByte(item, '=')
		if idx < 1 {
			return nil, fmt.Errorf("invalid --var %q; expected key=value", item)
		}
		key, raw := item[:idx], item[idx+1:]
		value := parseCLIValue(raw)
		segments := strings.Split(key, ".")
		cur := merged
		for _, seg := range segments[:len(segments)-1] {
			v, ok := cur[seg]
			if !ok {
				n := map[string]any{}
				cur[seg] = n
				cur = n
				continue
			}
			m, ok := v.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("conflicting --var path in %q", item)
			}
			cur = m
		}
		cur[segments[len(segments)-1]] = value
	}
	return merged, nil
}
func parseCLIValue(s string) any {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err == nil {
		var extra any
		if err := dec.Decode(&extra); err == io.EOF {
			return normalizeDecodedJSON(v)
		}
	}
	return s
}
