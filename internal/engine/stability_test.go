package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type testFailingReporter struct {
	failEvent    bool
	failFinalize bool
	finalized    bool
	events       int
}

func (r *testFailingReporter) OnEvent(EngineEvent) error {
	r.events++
	if r.failEvent {
		return errors.New("event sink unavailable")
	}
	return nil
}

func (r *testFailingReporter) Finalize(SuiteResult) error {
	r.finalized = true
	if r.failFinalize {
		return errors.New("final report unavailable")
	}
	return nil
}

func TestReporterEventFailureIsReturnedAfterFinalize(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "suites", "simple.yml")
	mustWrite(t, path, `formatVersion: "1.0"
kind: testSuite
info:
  name: reporter
tests:
  - id: one
    steps:
      - id: wait
        wait:
          forMs: 1
`)
	reporter := &testFailingReporter{failEvent: true}
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	result, err := New().Run(context.Background(), path, "", reporter, opts)
	if err == nil || !strings.Contains(err.Error(), "reporter event") {
		t.Fatalf("expected reporter event failure, got %v", err)
	}
	if !reporter.finalized {
		t.Fatal("Finalize must still be attempted after an event sink failure")
	}
	if result.Status != Passed || result.StatusCode != 0 {
		t.Fatalf("test execution result was lost: %#v", result)
	}
	if reporter.events != 1 {
		t.Fatalf("after first reporter failure, further event writes should stop; events=%d", reporter.events)
	}
}

func TestReporterFinalizeFailureIsReturnedWithResult(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "suites", "simple.yml")
	mustWrite(t, path, `formatVersion: "1.0"
kind: testSuite
info:
  name: reporter-finalize
tests:
  - id: one
    steps:
      - id: wait
        wait:
          forMs: 1
`)
	reporter := &testFailingReporter{failFinalize: true}
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	result, err := New().Run(context.Background(), path, "", reporter, opts)
	if err == nil || !strings.Contains(err.Error(), "reporter finalize") {
		t.Fatalf("expected finalize failure, got %v", err)
	}
	if result.Status != Passed || result.StatusCode != 0 || !reporter.finalized {
		t.Fatalf("unexpected result/finalize state: result=%#v reporter=%#v", result, reporter)
	}
}

func TestShortSensitiveValuesAreAlwaysRedactedConservatively(t *testing.T) {
	r := NewRedactor()
	r.Add("a")
	r.Add("42")

	cases := map[string]string{
		"pin=42":                  "pin=" + RedactionValue,
		"value=a":                 RedactionValue,
		"token: 42; next=a":       "token: " + RedactionValue + "; next=" + RedactionValue,
		"embedded=prefix42suffix": RedactionValue,
		"data alphabet":           RedactionValue,
	}
	for input, expected := range cases {
		if got := r.String(input); got != expected {
			t.Fatalf("short secret redaction: input=%q got=%q expected=%q", input, got, expected)
		}
	}
	redactedURL := r.URL("https://example.test/a/resource?pin=42&normal=data")
	if strings.Contains(redactedURL, "/a/") || strings.Contains(redactedURL, "42") || strings.Contains(redactedURL, "data") {
		t.Fatalf("short secret leaked from URL: %s", redactedURL)
	}
}

func TestSensitiveExtractionRedactsFirstResponseLog(t *testing.T) {
	const secret = "42"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"pin": secret, "message": "prefix" + secret + "suffix"})
	}))
	defer server.Close()

	root := t.TempDir()
	path := filepath.Join(root, "suites", "redaction.yml")
	mustWrite(t, path, `formatVersion: "1.0"
kind: testSuite
info:
  name: redaction
  tests: []
`)
	// Replace the compact placeholder suite with a real one. Keeping the YAML
	// construction here explicit makes this test independent of request files.
	mustWrite(t, path, `formatVersion: "1.0"
kind: testSuite
info:
  name: redaction
tests:
  - id: one
    steps:
      - id: get
        request:
          method: GET
          url: "`+server.URL+`"
        log:
          response:
            body: true
        extract:
          pin:
            from: $.pin
            sensitive: true
        expect:
          status: 200
`)
	reporter := &SummaryReporter{}
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	result, err := New().Run(context.Background(), path, "", reporter, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Passed {
		t.Fatalf("unexpected result: %#v", result)
	}
	foundResponseLog := false
	for _, event := range reporter.Events {
		if event.EventType != "response.log" {
			continue
		}
		foundResponseLog = true
		body := fmt.Sprint(event.Payload["body"])
		if strings.Contains(body, secret) {
			t.Fatalf("sensitive extraction leaked in first response log: %q", body)
		}
	}
	if !foundResponseLog {
		t.Fatal("expected response.log event")
	}
}

func TestNamedCasesExpandDeterministicallyAndExecuteIndependently(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"role": req.URL.Query().Get("role")})
	}))
	defer server.Close()

	root := t.TempDir()
	path := filepath.Join(root, "suites", "cases.yml")
	mustWrite(t, path, `formatVersion: "1.0"
kind: testSuite
id: cases
info:
  name: Named cases
tests:
  - id: authorization
    name: Authorization
    variables:
      inherited: base
    cases:
      guest:
        variables:
          role: guest
      admin:
        variables:
          role: admin
    steps:
      - id: echo
        request:
          method: GET
          url: "`+server.URL+`?role={{role}}"
        expect:
          status: 200
          body:
            path: $.role
            equals: "{{role}}"
`)
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	result, err := New().Run(context.Background(), path, "", &SummaryReporter{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Passed || len(result.Tests) != 2 {
		t.Fatalf("unexpected case result: %#v", result)
	}
	ids := []string{result.Tests[0].ID, result.Tests[1].ID}
	if !reflect.DeepEqual(ids, []string{"authorization[admin]", "authorization[guest]"}) {
		t.Fatalf("case IDs must be stable and sorted, got %#v", ids)
	}
	if result.Tests[0].BaseID != "authorization" || result.Tests[0].Case != "admin" {
		t.Fatalf("case metadata missing: %#v", result.Tests[0])
	}
}

func TestTestTimeoutStillRunsAfterEachAndAfterAll(t *testing.T) {
	var mu sync.Mutex
	afterEachCalls, afterAllCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch req.URL.Path {
		case "/after-each":
			afterEachCalls++
		case "/after-all":
			afterAllCalls++
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	root := t.TempDir()
	path := filepath.Join(root, "suites", "timeout-cleanup.yml")
	mustWrite(t, path, `formatVersion: "1.0"
kind: testSuite
info:
  name: timeout cleanup
hooks:
  afterEach:
    - id: after-each
      request:
        method: POST
        url: "`+server.URL+`/after-each"
      expect:
        status: 204
  afterAll:
    - id: after-all
      request:
        method: POST
        url: "`+server.URL+`/after-all"
      expect:
        status: 204
tests:
  - id: times-out
    timeoutMs: 20
    steps:
      - id: wait-too-long
        wait:
          forMs: 200
`)
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	opts.CleanupTimeoutMS = 250
	result, err := New().Run(context.Background(), path, "", &SummaryReporter{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Failed || result.StatusCode != -1 || len(result.Tests) != 1 || result.Tests[0].Status != Failed {
		t.Fatalf("timeout must fail test and suite: %#v", result)
	}
	if !strings.Contains(result.Tests[0].Error, "deadline exceeded") {
		t.Fatalf("test timeout reason missing: %#v", result.Tests[0])
	}
	mu.Lock()
	defer mu.Unlock()
	if afterEachCalls != 1 || afterAllCalls != 1 {
		t.Fatalf("cleanup calls afterEach=%d afterAll=%d", afterEachCalls, afterAllCalls)
	}
}

func TestCleanupContextIsBounded(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "suites", "cleanup-deadline.yml")
	mustWrite(t, path, `formatVersion: "1.0"
kind: testSuite
info:
  name: bounded cleanup
hooks:
  afterAll:
    - id: slow-cleanup
      wait:
        forMs: 500
tests:
  - id: pass
    steps:
      - id: quick
        wait:
          forMs: 1
`)
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	opts.CleanupTimeoutMS = 20
	started := time.Now()
	result, err := New().Run(context.Background(), path, "", &SummaryReporter{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(started); elapsed > 250*time.Millisecond {
		t.Fatalf("bounded cleanup took too long: %s", elapsed)
	}
	if result.Status != Failed || len(result.AfterAll) != 1 || result.AfterAll[0].Status != Failed {
		t.Fatalf("cleanup deadline should be visible as hook failure: %#v", result)
	}
}

func TestSuiteTimeoutIsReportedInJUnit(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "suites", "suite-timeout.yml")
	mustWrite(t, path, `formatVersion: "1.0"
kind: testSuite
info:
  name: suite deadline
timeoutMs: 20
tests:
  - id: slow
    steps:
      - id: wait
        wait:
          forMs: 200
`)
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	result, err := New().Run(context.Background(), path, "", &SummaryReporter{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Failed || result.Error == "" {
		t.Fatalf("suite timeout should be represented on suite result: %#v", result)
	}
	junit := filepath.Join(root, "junit.xml")
	if err := WriteJUnit(junit, []SuiteResult{result}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(junit)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `name="suite-runtime"`) || !strings.Contains(string(data), `type="veriflow.suite"`) {
		t.Fatalf("suite runtime failure missing from JUnit:\n%s", data)
	}
}

func TestAdvancedJSONPathDialect(t *testing.T) {
	doc := map[string]any{
		"users": []any{
			map[string]any{"id": 1, "name": "alice", "age": 30, "active": true, "meta": map[string]any{"code": "A1"}},
			map[string]any{"id": 2, "name": "bob", "age": 19, "active": true, "meta": map[string]any{"code": "B2"}},
			map[string]any{"id": 3, "name": "carol", "age": 42, "active": false, "meta": map[string]any{"code": "C3"}},
		},
		"dotted.key": map[string]any{"id": 99},
		"minimumAge": 21,
	}
	assertSelector := func(selector string, want any) {
		t.Helper()
		got, err := Select(doc, selector)
		if err != nil {
			t.Fatalf("%s: %v", selector, err)
		}
		if got.Missing || !reflect.DeepEqual(got.Value, want) {
			t.Fatalf("%s got=%#v want=%#v", selector, got.Value, want)
		}
	}
	assertSelector(`$.users[-1].name`, "carol")
	assertSelector(`$.users[0:3:2].name`, []any{"alice", "carol"})
	assertSelector(`$.users[::-1].name`, []any{"carol", "bob", "alice"})
	assertSelector(`$.users[0,2].id`, []any{1, 3})
	assertSelector(`$['dotted.key'].id`, 99)
	assertSelector(`$.users[?(@.age >= 21 && @.active == true)].name`, "alice")
	assertSelector(`$.users[?(@.name =~ /^A/i)].name`, "alice")
	assertSelector(`$..['code']`, []any{"A1", "B2", "C3"})
	assertSelector(`$..['id','code']`, []any{99, 1, "A1", 2, "B2", 3, "C3"})
	assertSelector(`$.users[?(@.age >= $.minimumAge)].name`, []any{"alice", "carol"})
	ids, err := Select(doc, `$..id`)
	if err != nil || ids.Missing {
		t.Fatalf("recursive id selector failed: %#v err=%v", ids, err)
	}
	if !IsSupportedJSONPath(`$.users[?(@.age >= 21 || @.name == 'bob')]`) {
		t.Fatal("valid filter selector was rejected")
	}
	for _, bad := range []string{
		`$.users[?(@.age == garbage)]`,
		`$.users[?(@.name =~ /abc/z)]`,
		`$.users[0:2:0]`,
		`$..[0]`,
	} {
		if IsSupportedJSONPath(bad) {
			t.Fatalf("invalid selector accepted: %s", bad)
		}
	}
}

func TestProjectConfigValidationAndValues(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ProjectConfigFile), `runtime:
  runTimeoutMs: 1000
  suiteTimeoutMs: 900
  testTimeoutMs: 800
  cleanupTimeoutMs: 700
  maxResponseBytes: 4096
  artifactsRoot: artifacts-ci
reports:
  json: reports/suite.json
ci:
  failIfNoTests: true
network:
  cookieJar: true
`)
	cfg, diagnostics := LoadProjectConfig(root)
	if len(diagnostics) != 0 {
		t.Fatalf("unexpected config diagnostics: %#v", diagnostics)
	}
	if cfg.Runtime.TestTimeoutMS != 800 || cfg.Runtime.CleanupTimeoutMS != 700 || cfg.Runtime.MaxResponseBytes != 4096 || cfg.Runtime.ArtifactsRoot != "artifacts-ci" || cfg.Reports.JSON != "reports/suite.json" || !cfg.CI.FailIfNoTests || !cfg.Network.CookieJar {
		t.Fatalf("unexpected config: %#v", cfg)
	}

	mustWrite(t, filepath.Join(root, ProjectConfigFile), `runtime:
  mystery: 1
`)
	_, diagnostics = LoadProjectConfig(root)
	if len(diagnostics) != 1 || !DiagnosticMatches(diagnostics[0], "project-config-unknown-field") || diagnostics[0].Code != "VF1602" {
		t.Fatalf("unknown config field must be stable diagnostic: %#v", diagnostics)
	}
}

func TestUserSpecFailuresHaveStableDiagnostics(t *testing.T) {
	root := t.TempDir()
	missingVersion := filepath.Join(root, "suites", "missing-version.yml")
	mustWrite(t, missingVersion, `kind: testSuite
tests: []
`)
	_, err := loadDocument(missingVersion)
	if err == nil {
		t.Fatal("expected load error")
	}
	d, ok := DiagnosticFromError(err, missingVersion)
	if !ok || d.Code != "VF1008" || d.Name != "missing-format-version" {
		t.Fatalf("unexpected diagnostic: %#v err=%v", d, err)
	}

	request := filepath.Join(root, "requests", "bad.yml")
	mustWrite(t, request, `formatVersion: "1.0"
kind: requestDefinition
id: bad
request:
  url: https://example.test
`)
	doc, err := loadDocument(request)
	if err != nil {
		t.Fatalf("semantic request error should load and validate, got %v", err)
	}
	vr := ValidationResult{Diagnostics: validateDocument(doc)}
	found := false
	for _, diagnostic := range vr.Diagnostics {
		if DiagnosticMatches(diagnostic, "invalid-http-method") && diagnostic.Code == "VF1302" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing method should be semantic VF1302 diagnostic: %#v", vr.Diagnostics)
	}
}

func TestLogicalReportStatusCodes(t *testing.T) {
	passed := SuiteResult{SchemaVersion: ReportSchemaVersion, EngineVersion: EngineVersion, Name: "pass", Status: Passed, StatusCode: 0, PassedCount: 1, Tests: []TestResult{{ID: "p", Status: Passed}}}
	failed := SuiteResult{SchemaVersion: ReportSchemaVersion, EngineVersion: EngineVersion, Name: "fail", Status: Failed, StatusCode: -1, FailedCount: 1, Tests: []TestResult{{ID: "f", Status: Failed}}}
	if got := Consolidate([]SuiteResult{passed}); got.StatusCode != 0 || got.OverallStatus != Passed {
		t.Fatalf("passed consolidated result: %#v", got)
	}
	if got := Consolidate([]SuiteResult{passed, failed}); got.StatusCode != -1 || got.OverallStatus != Failed {
		t.Fatalf("failed consolidated result: %#v", got)
	}
	path := filepath.Join(t.TempDir(), "run.json")
	if err := WriteRunReport(path, []SuiteResult{passed, failed}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"statusCode": -1`) {
		t.Fatalf("logical pipeline status missing from report: %s", data)
	}
}

func TestRedactorCoversSuiteAndTestLevelErrorsForShortSecrets(t *testing.T) {
	r := NewRedactor()
	r.Add("42")
	result := SuiteResult{
		Error: "suite failed for token 42",
		Tests: []TestResult{{ID: "one", Error: "test failed for token=42"}},
	}
	redacted := r.SuiteResult(result)
	if strings.Contains(redacted.Error, "42") || strings.Contains(redacted.Tests[0].Error, "42") {
		t.Fatalf("short secret escaped suite/test error redaction: %#v", redacted)
	}
	if !strings.Contains(redacted.Error, RedactionValue) || !strings.Contains(redacted.Tests[0].Error, RedactionValue) {
		t.Fatalf("redaction marker missing: %#v", redacted)
	}
}

func TestBadReferencedRequestProducesItsOwnStableDiagnostic(t *testing.T) {
	root := t.TempDir()
	suitePath := filepath.Join(root, "suites", "one.yml")
	mustWrite(t, suitePath, `formatVersion: "1.0"
kind: testSuite
info:
  name: one
tests:
  - id: one
    steps:
      - id: call
        use: "broken/get"
`)
	mustWrite(t, filepath.Join(root, "requests", "broken", "get.yml"), `kind: requestDefinition
request:
  method: GET
  url: https://example.test
`)
	result, err := New().Validate(suitePath, "", root)
	if err != nil {
		t.Fatal(err)
	}
	foundPrecise := false
	for _, d := range result.Diagnostics {
		if d.Code == "VF1008" && strings.HasSuffix(filepath.ToSlash(d.Location.File), "/requests/broken/get.yml") {
			foundPrecise = true
		}
	}
	if !foundPrecise {
		t.Fatalf("referenced document load diagnostic missing: %#v", result.Diagnostics)
	}
}

func TestProjectDiscoveryFailuresAreUserDiagnostics(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	_, err := New().Discover(missing)
	if err == nil {
		t.Fatal("expected missing project root to fail")
	}
	d, ok := DiagnosticFromError(err, missing)
	if !ok || d.Code != "VF1604" || d.Name != "project-root-not-found" {
		t.Fatalf("unexpected project discovery diagnostic: %#v err=%v", d, err)
	}
}

func TestNamedCaseIDsAndTypesAreStrictlyValidated(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "suites", "cases.yml")
	mustWrite(t, path, `formatVersion: "1.0"
kind: testSuite
info:
  name: cases
tests:
  - id: matrix
    cases:
      "bad case":
        name: 42
        variables: {}
    steps:
      - id: wait
        wait:
          forMs: 1
`)
	result, err := New().Validate(path, "", root)
	if err != nil {
		t.Fatal(err)
	}
	var invalidID, invalidName bool
	for _, d := range result.Diagnostics {
		invalidID = invalidID || DiagnosticMatches(d, "invalid-test-case")
		invalidName = invalidName || (DiagnosticMatches(d, "invalid-type") && strings.Contains(d.Location.DocumentPath, ".name"))
	}
	if !invalidID || !invalidName {
		t.Fatalf("expected strict case id/name diagnostics, got %#v", result.Diagnostics)
	}
}

func TestReportingImplementationsExerciseSuccessAndFailurePaths(t *testing.T) {
	root := t.TempDir()
	eventPath := filepath.Join(root, "events", "events.jsonl")
	live := &LiveReporter{EventJSONLPath: eventPath}
	if err := live.OnEvent(NewEvent("test.started", map[string]any{"id": "one"})); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(eventPath)
	if err != nil || !strings.Contains(string(data), `"event_type":"test.started"`) {
		t.Fatalf("event JSONL missing: %q err=%v", data, err)
	}

	jsonPath := filepath.Join(root, "report", "suite.json")
	fileReporter := &JSONFileReporter{OutputPath: jsonPath}
	if err := fileReporter.OnEvent(NewEvent("suite.started", map[string]any{"name": "one"})); err != nil {
		t.Fatal(err)
	}
	if err := fileReporter.Finalize(SuiteResult{Name: "one", Status: Passed, StatusCode: 0}); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(jsonPath)
	if err != nil || !strings.Contains(string(data), `"events"`) || !strings.Contains(string(data), `"result"`) {
		t.Fatalf("JSONFileReporter output missing: %q err=%v", data, err)
	}

	blocked := filepath.Join(root, "blocked")
	mustWrite(t, blocked, "not a directory")
	if err := (&LiveReporter{EventJSONLPath: filepath.Join(blocked, "events.jsonl")}).OnEvent(NewEvent("x", nil)); err == nil {
		t.Fatal("event reporter I/O error must propagate")
	}
}

func TestProjectConfigSchemaIsStrict(t *testing.T) {
	schema := ProjectConfigJSONSchema()
	if schema["additionalProperties"] != false {
		t.Fatalf("project config schema must reject unknown root fields: %#v", schema)
	}
	b, err := ProjectConfigJSONSchemaBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"runTimeoutMs"`) || !strings.Contains(string(b), `"artifactsRoot"`) || !strings.Contains(string(b), `"consolidatedJson"`) || !strings.Contains(string(b), `"json"`) {
		t.Fatalf("project config schema incomplete: %s", b)
	}
}

func TestRedactorMasksSensitiveNumericAndBooleanStructuredValues(t *testing.T) {
	r := NewRedactor()
	r.Add(42)
	r.Add(true)
	got := r.Any(map[string]any{
		"pin":    42,
		"active": true,
		"other":  7,
	})
	m, ok := got.(map[string]any)
	if !ok {
		t.Fatalf("redacted value type=%T", got)
	}
	if m["pin"] != RedactionValue || m["active"] != RedactionValue || m["other"] != 7 {
		t.Fatalf("structured scalar redaction failed: %#v", m)
	}
}

func TestMalformedProjectConfigUsesStableYAMLDiagnostic(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, ProjectConfigFile), "- not-a-mapping\n")
	_, diagnostics := LoadProjectConfig(root)
	if len(diagnostics) != 1 || diagnostics[0].Code != "VF1001" || diagnostics[0].Name != "yaml-parse-error" {
		t.Fatalf("malformed project config diagnostic=%#v", diagnostics)
	}
}
