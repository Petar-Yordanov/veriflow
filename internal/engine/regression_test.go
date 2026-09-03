package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestResolveVariablesRecursiveAndCycle(t *testing.T) {
	lookup := map[string]any{
		"baseApiUrl":   "http://localhost:5000",
		"apiUrl":       "{{baseApiUrl}}",
		"owner":        "{{defaultOwner}}",
		"defaultOwner": "abc-123",
	}
	resolved, err := ResolveVariables(lookup)
	if err != nil {
		t.Fatal(err)
	}
	if resolved["apiUrl"] != "http://localhost:5000" {
		t.Fatalf("apiUrl = %#v", resolved["apiUrl"])
	}
	if resolved["owner"] != "abc-123" {
		t.Fatalf("owner = %#v", resolved["owner"])
	}

	_, err = ResolveVariables(map[string]any{"a": "{{b}}", "b": "{{a}}"})
	if err == nil || !strings.Contains(err.Error(), "Variable expansion cycle detected: a -> b -> a") {
		t.Fatalf("unexpected cycle error: %v", err)
	}
}

func TestResolveRequestReferenceUsesRequestsConvention(t *testing.T) {
	root := t.TempDir()
	got, err := ResolveRequestReference(root, "feedback/get-by-id")
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "requests", "feedback", "get-by-id.yml")
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
	if _, err := ResolveRequestReference(root, "../requests/feedback/get-by-id.yml"); err == nil {
		t.Fatal("expected legacy ../ request reference to be rejected")
	}
}

func TestEnvironmentSelectionByNameOnly(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "environments"))
	mustWrite(t, filepath.Join(root, "environments", "dev.yml"), "formatVersion: \"1.0\"\nkind: environment\nname: dev\nvariables: {}\n")
	got, err := SelectEnvironment(root, "dev")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(got) != "dev.yml" {
		t.Fatalf("got %s", got)
	}
	if _, err := SelectEnvironment(root, filepath.Join(root, "environments", "dev.yml")); err == nil {
		t.Fatal("expected path selector rejection")
	}
}

func TestExternalBodyFileIsSent(t *testing.T) {
	root := t.TempDir()
	payload := []byte{0, 1, 2, 3, 4, 255}
	mustWriteBytes(t, filepath.Join(root, "fixtures", "payload.bin"), payload)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if string(b) != string(payload) {
			t.Errorf("payload mismatch: %v", b)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	req := RequestSpec{Method: "POST", URL: server.URL, BodyFile: "fixtures/payload.bin", BodyFileMode: "binary"}
	prepared, err := PrepareRequest(req, map[string]any{}, root)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := SendHTTP(context.Background(), nil, prepared, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 204 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestAssertionExpectedValuesAreInterpolated(t *testing.T) {
	expect := &ExpectationSpec{Body: &AssertionClause{Path: "$.data.id", Operators: map[string]any{"equals": "{{supportRequestId}}"}}}
	resp := HTTPResponse{StatusCode: 200, JSON: map[string]any{"data": map[string]any{"id": "abc"}}}
	out, err := EvaluateAssertions(expect, resp, map[string]float64{}, resp.JSON, map[string]any{"supportRequestId": "abc"})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Passed {
		t.Fatalf("assertion should pass: %#v", out.Evaluations)
	}
}

func TestRunnerRecursiveVariablesExtractedAliasAndAssertionInterpolation(t *testing.T) {
	const ownerID = "00000000-0000-0000-0000-000000000001"
	const createdID = "4e74f0bb-6a6d-4f3c-a6c9-4ebc71d0d6dd"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/resources":
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["ownerId"] != ownerID {
				t.Errorf("ownerId unresolved: %#v", body["ownerId"])
			}
			w.WriteHeader(201)
			_, _ = io.WriteString(w, `{"data":{"id":"`+createdID+`"}}`)
		case r.Method == "GET" && r.URL.Path == "/api/resources/"+createdID:
			_, _ = io.WriteString(w, `{"data":{"id":"`+createdID+`","status":"Open"}}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "environments"))
	mustMkdir(t, filepath.Join(root, "requests", "resources"))
	mustMkdir(t, filepath.Join(root, "suites"))
	mustWrite(t, filepath.Join(root, "environments", "dev.yml"), `formatVersion: "1.0"
kind: environment
name: dev
variables:
  localApiUrl: "`+server.URL+`"
  apiUrl: "{{localApiUrl}}"
  defaultOwnerId: "`+ownerID+`"
`)
	mustWrite(t, filepath.Join(root, "requests", "resources", "create.yml"), `formatVersion: "1.0"
kind: requestDefinition
id: resources/create
request:
  method: POST
  baseUrl: "{{apiUrl}}"
  path: "/api/resources"
  body:
    ownerId: "{{resourceOwnerId}}"
outputs:
  createdResourceId:
    path: "$.data.id"
`)
	mustWrite(t, filepath.Join(root, "requests", "resources", "get.yml"), `formatVersion: "1.0"
kind: requestDefinition
id: resources/get
request:
  method: GET
  baseUrl: "{{apiUrl}}"
  path: "/api/resources/{resourceId}"
  pathParams:
    resourceId: "{{resourceId}}"
`)
	mustWrite(t, filepath.Join(root, "suites", "smoke.yml"), `formatVersion: "1.0"
kind: testSuite
info:
  name: Smoke
tests:
  - id: smoke
    steps:
      - id: create
        use: resources/create
        variables:
          resourceOwnerId: "{{defaultOwnerId}}"
        expect:
          status: 201
        extract:
          createdResourceId:
            fromDefinition: createdResourceId
            scope: test
      - id: get
        use: resources/get
        variables:
          resourceId: "{{createdResourceId}}"
        expect:
          status: 200
          body:
            and:
              - path: "$.data.id"
                equals: "{{createdResourceId}}"
              - path: "$.data.status"
                equals: Open
`)
	env, err := SelectEnvironment(root, "dev")
	if err != nil {
		t.Fatal(err)
	}
	reporter := &SummaryReporter{}
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	result, err := New().Run(context.Background(), filepath.Join(root, "suites", "smoke.yml"), env, reporter, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Passed {
		b, _ := json.MarshalIndent(result, "", "  ")
		t.Fatalf("run failed:\n%s", b)
	}
}

func TestConsolidatedResults(t *testing.T) {
	r := Consolidate([]SuiteResult{{Status: Passed, PassedCount: 3, Tests: make([]TestResult, 3)}, {Status: Failed, PassedCount: 1, FailedCount: 1, Tests: make([]TestResult, 2)}, {Status: Passed, PassedCount: 5, Tests: make([]TestResult, 5)}})
	if r.Suites != 3 || r.TotalTests != 10 || r.Passed != 9 || r.Failed != 1 || r.OverallStatus != Failed {
		t.Fatalf("unexpected consolidation: %#v", r)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
}
func mustWrite(t *testing.T, p, s string) { t.Helper(); mustWriteBytes(t, p, []byte(s)) }
func mustWriteBytes(t *testing.T, p string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, b, 0644); err != nil {
		t.Fatal(err)
	}
}

func TestYAMLFlowCollectionsAcceptBareYAMLScalars(t *testing.T) {
	raw, _, err := parseYAML([]byte(`formatVersion: "1.0"
kind: testSuite
info:
  name: Flow collections
tests:
  - id: flow
    tags: [smoke, pipeline]
    steps:
      - id: request
        request:
          method: GET
          url: http://example.invalid
        expect:
          status: [200, 204]
          body:
            path: $.status
            in: [Open, Closed]
`))
	if err != nil {
		t.Fatal(err)
	}
	typed, err := parseTypedDocument("flow.yml", raw)
	if err != nil {
		t.Fatal(err)
	}
	suite := typed.(TestSuiteSpec)
	if got := suite.Tests[0].Tags; len(got) != 2 || got[0] != "smoke" || got[1] != "pipeline" {
		t.Fatalf("tags parsed incorrectly: %#v", got)
	}
	status := suite.Tests[0].Steps[0].Expect.Status
	if got := asIntSlice(status); len(got) != 2 || got[0] != 200 || got[1] != 204 {
		t.Fatalf("status parsed incorrectly: %#v", status)
	}
	in := suite.Tests[0].Steps[0].Expect.Body.Operators["in"]
	values := asStringSlice(in)
	if len(values) != 2 || values[0] != "Open" || values[1] != "Closed" {
		t.Fatalf("bare scalar flow sequence parsed incorrectly: %#v", in)
	}
}

func TestYAMLBlockScalars(t *testing.T) {
	raw, _, err := parseYAML([]byte(`formatVersion: "1.0"
kind: testSuite
info:
  name: block scalars
tests:
  - id: block
    steps:
      - id: literal
        request:
          method: POST
          url: http://example.invalid
          bodyRaw: |
            line one
            line two
      - id: folded
        request:
          method: POST
          url: http://example.invalid
          bodyRaw: >-
            hello
            world
`))
	if err != nil {
		t.Fatal(err)
	}
	typed, err := parseTypedDocument("block.yml", raw)
	if err != nil {
		t.Fatal(err)
	}
	suite := typed.(TestSuiteSpec)
	if got := suite.Tests[0].Steps[0].Request.BodyRaw; got != "line one\nline two\n" {
		t.Fatalf("literal block = %q", got)
	}
	if got := suite.Tests[0].Steps[1].Request.BodyRaw; got != "hello world" {
		t.Fatalf("folded block = %q", got)
	}
}

func TestRequestInputContracts(t *testing.T) {
	def := RequestDefinitionSpec{Inputs: map[string]InputDefinition{
		"id":    {Type: "integer", Required: true},
		"token": {Type: "string", Required: true},
		"limit": {Type: "integer", Default: 10},
	}}
	if err := ValidateRequestInputs(def, map[string]any{"id": int64(4), "token": "secret", "limit": 20}); err != nil {
		t.Fatalf("valid inputs rejected: %v", err)
	}
	if err := ValidateRequestInputs(def, map[string]any{"id": int64(4)}); err == nil || !strings.Contains(err.Error(), "required input") {
		t.Fatalf("expected missing required input error, got %v", err)
	}
	if err := ValidateRequestInputs(def, map[string]any{"id": "not-an-int", "token": "secret"}); err == nil || !strings.Contains(err.Error(), "must be integer") {
		t.Fatalf("expected input type error, got %v", err)
	}
	if err := ValidateRequestInputs(def, map[string]any{"id": int64(4), "token": "secret", "unknown": true}); err == nil || !strings.Contains(err.Error(), "unknown input") {
		t.Fatalf("expected unknown input error, got %v", err)
	}
}

func TestBodyFileTextAndJSONInterpolation(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "payload.txt"), "hello {{name}} from {{nested.place}}")
	mustWrite(t, filepath.Join(root, "payload.json"), `{"name":"{{name}}","count":"{{count}}","enabled":"{{enabled}}"}`)
	lookup := map[string]any{"name": "pipeline", "count": 7, "enabled": true, "nested": map[string]any{"place": "CI"}}

	textReq := RequestSpec{Method: "POST", URL: "http://example.invalid", BodyFile: "payload.txt", BodyFileMode: "text"}
	textPrepared, err := PrepareRequest(textReq, lookup, root)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(textPrepared.Content); got != "hello pipeline from CI" {
		t.Fatalf("text body = %q", got)
	}

	jsonReq := RequestSpec{Method: "POST", URL: "http://example.invalid", BodyFile: "payload.json", BodyFileMode: "json"}
	jsonPrepared, err := PrepareRequest(jsonReq, lookup, root)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(jsonPrepared.Content, &body); err != nil {
		t.Fatal(err)
	}
	if body["name"] != "pipeline" || body["count"] != float64(7) || body["enabled"] != true {
		t.Fatalf("unexpected interpolated JSON: %#v", body)
	}
}

func TestResponseBodySizeLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("x", 128))
	}))
	defer server.Close()
	_, err := SendHTTPWithOptions(context.Background(), nil, PreparedRequest{Method: http.MethodGet, URL: server.URL}, SendHTTPOptions{MaxResponseBytes: 64})
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("expected response limit error, got %v", err)
	}
}

func TestSuiteDefaultsAndRepeatIterationIndex(t *testing.T) {
	seen := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Query().Get("iteration"))
		if r.Header.Get("X-Default") != "suite" {
			t.Errorf("suite default header not applied: %q", r.Header.Get("X-Default"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true}`)
	}))
	defer server.Close()

	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "suites"))
	mustWrite(t, filepath.Join(root, "suites", "repeat.yml"), `formatVersion: "1.0"
kind: testSuite
info:
  name: Repeat
defaults:
  headers:
    X-Default: suite
  timeoutMs: 1000
tests:
  - id: repeat
    steps:
      - id: call
        repeat:
          warmupCount: 1
          count: 3
        request:
          method: GET
          url: "`+server.URL+`"
          query:
            iteration: "{{iterationIndex}}"
        expect:
          status: 200
`)
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	result, err := New().Run(context.Background(), filepath.Join(root, "suites", "repeat.yml"), "", &SummaryReporter{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Passed {
		t.Fatalf("result = %#v", result)
	}
	want := []string{"-1", "0", "1", "2"}
	if strings.Join(seen, ",") != strings.Join(want, ",") {
		t.Fatalf("iterations = %#v want %#v", seen, want)
	}
}

func TestRunIDStableAcrossSteps(t *testing.T) {
	ids := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ids = append(ids, r.Header.Get("X-Run-ID"))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "suites"))
	mustWrite(t, filepath.Join(root, "suites", "runid.yml"), `formatVersion: "1.0"
kind: testSuite
info:
  name: Run ID
tests:
  - id: id
    steps:
      - id: one
        request:
          method: GET
          url: "`+server.URL+`"
          headers:
            X-Run-ID: "{{runId}}"
        expect:
          status: 204
      - id: two
        request:
          method: GET
          url: "`+server.URL+`"
          headers:
            X-Run-ID: "{{runId}}"
        expect:
          status: 204
`)
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	res, err := New().Run(context.Background(), filepath.Join(root, "suites", "runid.yml"), "", &SummaryReporter{}, opts)
	if err != nil || res.Status != Passed {
		t.Fatalf("run failed: %v %#v", err, res)
	}
	if len(ids) != 2 || ids[0] == "" || ids[0] != ids[1] {
		t.Fatalf("run IDs not stable: %#v", ids)
	}
}

func TestSensitiveInputRedactedFromEventsResultsAndArtifacts(t *testing.T) {
	const secret = "pipeline-secret-123"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Errorf("server did not receive real secret")
		}
		w.Header().Set("X-Secret-Token", secret)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"token":"`+secret+`"}}`)
	}))
	defer server.Close()

	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "requests", "secure"))
	mustMkdir(t, filepath.Join(root, "suites"))
	mustWrite(t, filepath.Join(root, "requests", "secure", "get.yml"), `formatVersion: "1.0"
kind: requestDefinition
id: secure/get
inputs:
  token:
    type: string
    required: true
    sensitive: true
request:
  method: GET
  url: "`+server.URL+`?token={{inputs.token}}"
  headers:
    Authorization: "Bearer {{inputs.token}}"
`)
	mustWrite(t, filepath.Join(root, "suites", "secure.yml"), `formatVersion: "1.0"
kind: testSuite
info:
  name: Secure
tests:
  - id: secure
    steps:
      - id: call
        use: secure/get
        with:
          token: "`+secret+`"
        log:
          request:
            headers: true
            body: true
          response:
            headers: true
            body: true
        expect:
          status: 200
          body:
            path: "$.data.token"
            equals: "`+secret+`"
        extract:
          tokenAgain:
            from: "$.data.token"
            sensitive: true
        artifacts:
          saveResponseBodyTo: secure/body.json
          saveParsedJsonTo: secure/parsed.json
          saveHeadersTo: secure/headers.json
`)
	reporter := &SummaryReporter{}
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	res, err := New().Run(context.Background(), filepath.Join(root, "suites", "secure.yml"), "", reporter, opts)
	if err != nil || res.Status != Passed {
		t.Fatalf("run failed: %v %#v", err, res)
	}
	blob, _ := json.Marshal(struct {
		Events []EngineEvent `json:"events"`
		Result SuiteResult   `json:"result"`
	}{reporter.Events, res})
	if strings.Contains(string(blob), secret) {
		t.Fatalf("secret leaked into result/events: %s", blob)
	}
	if !strings.Contains(string(blob), RedactionValue) {
		t.Fatalf("expected redaction marker in result/events: %s", blob)
	}
	for _, rel := range []string{"secure/body.json", "secure/parsed.json", "secure/headers.json"} {
		b, err := os.ReadFile(filepath.Join(root, "artifacts", rel))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), secret) {
			t.Fatalf("secret leaked into artifact %s: %s", rel, b)
		}
	}
}

func TestValidateProjectChecksUnusedRequests(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "requests", "unused"))
	mustMkdir(t, filepath.Join(root, "suites"))
	mustWrite(t, filepath.Join(root, "requests", "unused", "bad.yml"), `formatVersion: "1.0"
kind: requestDefinition
id: unused/bad
request:
  method: BREW
  url: http://example.invalid
`)
	mustWrite(t, filepath.Join(root, "suites", "ok.yml"), `formatVersion: "1.0"
kind: testSuite
info:
  name: OK
tests: []
`)
	result, err := New().ValidateProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK() {
		t.Fatal("expected unused invalid request to make project validation fail")
	}
	found := false
	for _, d := range result.Diagnostics {
		if DiagnosticMatches(d, "invalid-http-method") && strings.Contains(d.Location.File, "bad.yml") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing invalid-http-method diagnostic: %#v", result.Diagnostics)
	}
}

func TestJUnitAndConsolidatedReport(t *testing.T) {
	dir := t.TempDir()
	suites := []SuiteResult{
		{SchemaVersion: ReportSchemaVersion, EngineVersion: EngineVersion, Name: "passing", Status: Passed, PassedCount: 1, Tests: []TestResult{{ID: "ok", Status: Passed, DurationMS: 2}}},
		{SchemaVersion: ReportSchemaVersion, EngineVersion: EngineVersion, Name: "failing", Status: Failed, FailedCount: 1, Tests: []TestResult{{ID: "bad", Status: Failed, DurationMS: 3, Steps: []StepResult{{ID: "call", Status: Failed, Error: "boom"}}}}},
	}
	junit := filepath.Join(dir, "junit.xml")
	report := filepath.Join(dir, "combined.json")
	if err := WriteJUnit(junit, suites); err != nil {
		t.Fatal(err)
	}
	if err := WriteRunReport(report, suites); err != nil {
		t.Fatal(err)
	}
	jb, _ := os.ReadFile(junit)
	if !strings.Contains(string(jb), `<testsuites`) || !strings.Contains(string(jb), `failures="1"`) || !strings.Contains(string(jb), "boom") {
		t.Fatalf("unexpected junit: %s", jb)
	}
	rb, _ := os.ReadFile(report)
	var got RunReport
	if err := json.Unmarshal(rb, &got); err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != ReportSchemaVersion || got.Result.TotalTests != 2 || got.Result.Failed != 1 {
		t.Fatalf("unexpected combined report: %#v", got)
	}
}

func TestNetworkErrorRetry(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		if calls < 3 {
			return nil, fmt.Errorf("temporary connection failure")
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"ok":true}`)), Request: r}, nil
	})}

	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "suites"))
	mustWrite(t, filepath.Join(root, "suites", "retry.yml"), `formatVersion: "1.0"
kind: testSuite
info:
  name: Network retry
tests:
  - id: retry
    steps:
      - id: call
        request:
          method: GET
          url: http://does-not-matter.invalid
        retry:
          count: 2
          delayMs: 1
          when:
            networkErrors: true
        expect:
          status: 200
`)
	e := New()
	e.HTTPClient = client
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	res, err := e.Run(context.Background(), filepath.Join(root, "suites", "retry.yml"), "", &SummaryReporter{}, opts)
	if err != nil || res.Status != Passed {
		t.Fatalf("run failed: %v %#v", err, res)
	}
	if calls != 3 {
		t.Fatalf("calls=%d want 3", calls)
	}
	step := res.Tests[0].Steps[0]
	if !step.RetrySummary.Retried || step.RetrySummary.Attempts != 3 {
		t.Fatalf("retry summary = %#v", step.RetrySummary)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestBodyFileSymlinkEscapeIsRejected(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret.txt")
	mustWrite(t, outside, "do-not-read")
	link := filepath.Join(root, "fixtures", "linked.txt")
	mustMkdir(t, filepath.Dir(link))
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	_, err := PrepareRequest(RequestSpec{Method: "POST", URL: "http://example.invalid", BodyFile: "fixtures/linked.txt", BodyFileMode: "text"}, map[string]any{}, root)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "symlink") {
		t.Fatalf("expected symlink escape rejection, got %v", err)
	}
}

func TestArtifactSymlinkEscapeIsRejected(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink not available: %v", err)
	}
	_, err := saveArtifact(root, "linked/output.txt", []byte("nope"))
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "symlink") {
		t.Fatalf("expected artifact symlink rejection, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(outside, "output.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("artifact was written outside root")
	}
}

func FuzzParseYAMLDoesNotPanic(f *testing.F) {
	for _, seed := range []string{
		"formatVersion: \"1.0\"\nkind: environment\nname: dev\nvariables: {}\n",
		"a: [one, two, three]\n",
		"bodyRaw: |\n  hello\n  world\n",
		"tests:\n  - id: x\n    steps: []\n",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, input string) {
		_, _, _ = parseYAML([]byte(input))
	})
}

func FuzzSelectorDoesNotPanic(f *testing.F) {
	for _, seed := range []string{"$", "$.data.id", "$.items[0]", "$.items[*]", "not-jsonpath", "$[999999999999999999999]"} {
		f.Add(seed)
	}
	doc := map[string]any{"data": map[string]any{"id": "x"}, "items": []any{1, 2, 3}}
	f.Fuzz(func(t *testing.T, selector string) {
		_, _ = Select(doc, selector)
	})
}

func TestFirstClassAuthModes(t *testing.T) {
	lookup := map[string]any{"token": "bearer-secret", "password": "basic-secret", "apiKey": "query-secret"}

	bearer, err := PrepareRequest(RequestSpec{Method: "GET", URL: "http://example.invalid", Auth: &AuthSpec{Type: "bearer", Token: "{{token}}"}}, lookup, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if bearer.Headers["Authorization"] != "Bearer bearer-secret" || len(bearer.SensitiveValues) != 1 {
		t.Fatalf("unexpected bearer auth: %#v", bearer)
	}

	basic, err := PrepareRequest(RequestSpec{Method: "GET", URL: "http://example.invalid", Auth: &AuthSpec{Type: "basic", Username: "user", Password: "{{password}}"}}, lookup, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(basic.Headers["Authorization"], "Basic ") || len(basic.SensitiveValues) < 1 {
		t.Fatalf("unexpected basic auth: %#v", basic)
	}

	apiKey, err := PrepareRequest(RequestSpec{Method: "GET", URL: "http://example.invalid/path", Auth: &AuthSpec{Type: "apiKey", Name: "key", Value: "{{apiKey}}", In: "query"}}, lookup, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(apiKey.URL, "key=query-secret") || len(apiKey.SensitiveValues) != 1 {
		t.Fatalf("unexpected api key auth: %#v", apiKey)
	}
}

func TestCustomCAHTTPClient(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cert := server.Certificate()
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	mustWriteBytes(t, caPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}))
	client, err := NewHTTPClient(HTTPClientConfig{CAFile: caPath})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := SendHTTPWithOptions(context.Background(), client, PreparedRequest{Method: http.MethodGet, URL: server.URL}, SendHTTPOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestHTTPClientRejectsIncompleteMTLSConfig(t *testing.T) {
	if _, err := NewHTTPClient(HTTPClientConfig{ClientCertFile: "cert.pem"}); err == nil || !strings.Contains(err.Error(), "provided together") {
		t.Fatalf("expected client cert/key pairing error, got %v", err)
	}
}

func TestSelectorQuotedPropertiesAndObjectWildcard(t *testing.T) {
	doc := map[string]any{
		"data": map[string]any{
			"dotted.key": map[string]any{"x-y": 7},
			"object":     map[string]any{"b": 2, "a": 1},
		},
	}

	result, err := Select(doc, `$.data['dotted.key']["x-y"]`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Missing || asInt(result.Value) != 7 {
		t.Fatalf("quoted property result = %#v", result)
	}

	result, err = Select(doc, `$.data.object.*`)
	if err != nil {
		t.Fatal(err)
	}
	got := asSlice(result.Value)
	if len(got) != 2 || asInt(got[0]) != 1 || asInt(got[1]) != 2 {
		t.Fatalf("object wildcard must be deterministic, got %#v", result.Value)
	}

	if IsSupportedJSONPath(`$.data['unterminated]`) {
		t.Fatal("invalid quoted selector should be rejected")
	}
}

func TestEnvironmentInheritanceDeepMergesVariables(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "environments", "base.yml"), `formatVersion: "1.0"
kind: environment
name: base
variables:
  baseUrl: http://example.test
  nested:
    one: 1
    shared: base
`)
	child := filepath.Join(root, "environments", "ci.yml")
	mustWrite(t, child, `formatVersion: "1.0"
kind: environment
name: ci
extends: base
variables:
  nested:
    two: 2
    shared: child
  alias: "{{baseUrl}}"
`)
	mustWrite(t, filepath.Join(root, "suites", "empty.yml"), `formatVersion: "1.0"
kind: testSuite
info:
  name: empty
tests: []
`)

	bundle, err := loadBundle(filepath.Join(root, "suites", "empty.yml"), child, root)
	if err != nil {
		t.Fatal(err)
	}
	env := bundle.Environment.Typed.(EnvironmentSpec)
	if env.Variables["baseUrl"] != "http://example.test" {
		t.Fatalf("base variable not inherited: %#v", env.Variables)
	}
	nested := asMap(env.Variables["nested"])
	if asInt(nested["one"]) != 1 || asInt(nested["two"]) != 2 || nested["shared"] != "child" {
		t.Fatalf("nested environment merge = %#v", nested)
	}
	resolved, err := ResolveVariables(env.Variables)
	if err != nil {
		t.Fatal(err)
	}
	if resolved["alias"] != "http://example.test" {
		t.Fatalf("inherited alias = %#v", resolved["alias"])
	}
}

func TestEnvironmentInheritanceCycleFailsProjectValidation(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "environments", "a.yml"), `formatVersion: "1.0"
kind: environment
name: a
extends: b
variables: {}
`)
	mustWrite(t, filepath.Join(root, "environments", "b.yml"), `formatVersion: "1.0"
kind: environment
name: b
extends: a
variables: {}
`)

	result, err := New().ValidateProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK() {
		t.Fatal("environment inheritance cycle should fail validation")
	}
	found := false
	for _, d := range result.Diagnostics {
		if DiagnosticMatches(d, "invalid-environment-inheritance") && strings.Contains(d.Message, "cycle") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("missing environment cycle diagnostic: %#v", result.Diagnostics)
	}
}

func TestRequestCollectionsCookiesAndMultipartMetadata(t *testing.T) {
	root := t.TempDir()
	mustWrite(t, filepath.Join(root, "fixtures", "upload.txt"), "hello")

	prepared, err := PrepareRequest(RequestSpec{
		Method:  "POST",
		BaseURL: "http://example.test",
		Path:    "/upload",
		Query: map[string]any{
			"tag": []any{"one", "two"},
		},
		Cookies: map[string]any{"session": "abc", "theme": "dark"},
		Multipart: map[string]any{
			"label": []any{"a", "b"},
			"document": map[string]any{
				"file":        "fixtures/upload.txt",
				"filename":    "custom name.txt",
				"contentType": "text/plain",
			},
		},
	}, map[string]any{}, root)
	if err != nil {
		t.Fatal(err)
	}

	u, err := url.Parse(prepared.URL)
	if err != nil {
		t.Fatal(err)
	}
	if got := u.Query()["tag"]; !reflect.DeepEqual(got, []string{"one", "two"}) {
		t.Fatalf("query values = %#v", got)
	}
	if !strings.Contains(prepared.Headers["Cookie"], "session=abc") || !strings.Contains(prepared.Headers["Cookie"], "theme=dark") {
		t.Fatalf("cookie header = %q", prepared.Headers["Cookie"])
	}

	req, err := http.NewRequest(http.MethodPost, prepared.URL, bytes.NewReader(prepared.Content))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", prepared.Headers["Content-Type"])
	if err := req.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	if got := req.MultipartForm.Value["label"]; !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("multipart repeated values = %#v", got)
	}
	file, hdr, err := req.FormFile("document")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if hdr.Filename != "custom name.txt" {
		t.Fatalf("filename=%q", hdr.Filename)
	}
	if hdr.Header.Get("Content-Type") != "text/plain" {
		t.Fatalf("content type=%q", hdr.Header.Get("Content-Type"))
	}
}

func TestFormSupportsRepeatedValues(t *testing.T) {
	prepared, err := PrepareRequest(RequestSpec{
		Method:  "POST",
		URL:     "http://example.test/form",
		Form:    map[string]any{"tag": []any{"a", "b"}},
		Headers: map[string]any{},
	}, map[string]any{}, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	values, err := url.ParseQuery(string(prepared.Content))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(values["tag"], []string{"a", "b"}) {
		t.Fatalf("form values=%#v", values["tag"])
	}
}

func TestExtractionSupportsHeaderCookieTextAndStatus(t *testing.T) {
	response := HTTPResponse{
		StatusCode: 202,
		Headers:    map[string]string{"X-Request-ID": "request-123"},
		Cookies:    map[string]string{"session": "cookie-456"},
		Body:       []byte("job=id-789 state=queued"),
	}
	specs := map[string]ExtractionSpec{
		"requestId": {FromHeader: "X-Request-ID", Scope: TestScope, Required: true},
		"session":   {FromCookie: "session", Scope: TestScope, Required: true, Sensitive: true},
		"jobId":     {FromTextRegex: `job=(id-[0-9]+)`, Scope: TestScope, Required: true},
		"status":    {FromStatus: true, Scope: TestScope, Required: true},
	}
	out, err := ExtractValues(specs, nil, response, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !out.OK || out.Variables["requestId"] != "request-123" || out.Variables["session"] != "cookie-456" || out.Variables["jobId"] != "id-789" || asInt(out.Variables["status"]) != 202 {
		t.Fatalf("unexpected extraction: %#v", out)
	}
}

func TestExtractionRequiresExactlyOneSource(t *testing.T) {
	root := t.TempDir()
	suite := filepath.Join(root, "suites", "bad.yml")
	mustWrite(t, suite, `formatVersion: "1.0"
kind: testSuite
info:
  name: bad extraction
tests:
  - id: bad
    steps:
      - id: bad
        request:
          method: GET
          url: http://example.test
        extract:
          value:
            from: $.value
            fromHeader: X-Value
`)
	result, err := New().Validate(suite, "", root)
	if err != nil {
		t.Fatal(err)
	}
	if result.OK() {
		t.Fatal("multiple extraction sources should fail validation")
	}
	found := false
	for _, d := range result.Diagnostics {
		if DiagnosticMatches(d, "extract-source") {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing extract-source diagnostic: %#v", result.Diagnostics)
	}
}

func TestSpecJSONSchemaIsVersionedAndContainsCoreDefinitions(t *testing.T) {
	b, err := SpecJSONSchemaBytes()
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if err := json.Unmarshal(b, &schema); err != nil {
		t.Fatal(err)
	}
	defs := asMap(schema["$defs"])
	for _, name := range []string{"request", "step", "test", "requestDefinition", "testSuite", "environment"} {
		if _, ok := defs[name]; !ok {
			t.Fatalf("schema missing definition %q", name)
		}
	}
	if schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Fatalf("unexpected schema dialect: %#v", schema["$schema"])
	}
}

func TestSeededBuiltinsAreDeterministicButDistinctPerStep(t *testing.T) {
	seedA := DeriveBuiltinSeed(42, "suite", "test", "step-a", 0)
	seedB := DeriveBuiltinSeed(42, "suite", "test", "step-b", 0)
	if seedA == seedB {
		t.Fatal("derived seeds for different steps must differ")
	}
	a1 := BuildBuiltinVariables(seedA, map[string]any{"runId": "fixed"})
	a2 := BuildBuiltinVariables(seedA, map[string]any{"runId": "fixed"})
	b := BuildBuiltinVariables(seedB, map[string]any{"runId": "fixed"})
	for _, key := range []string{"randomUuid", "randomInt", "randomString"} {
		if !reflect.DeepEqual(a1[key], a2[key]) {
			t.Fatalf("%s should be deterministic: %#v != %#v", key, a1[key], a2[key])
		}
	}
	if a1["randomString"] == b["randomString"] && a1["randomInt"] == b["randomInt"] {
		t.Fatal("different steps should not receive identical seeded random values")
	}
}

func TestAssertionUtilityOperatorsAndUniqueFalse(t *testing.T) {
	body := map[string]any{
		"name":   "Veriflow Pipeline",
		"items":  []any{"alpha", "alpha"},
		"nested": map[string]any{"a": 1, "b": 2},
	}
	expect := &ExpectationSpec{Body: &AssertionClause{And: []AssertionClause{
		{Path: "$.name", Operators: map[string]any{"equalsIgnoreCase": "veriflow pipeline", "startsWith": "Veriflow", "endsWith": "Pipeline", "length": 17, "minLength": 10, "maxLength": 30}},
		{Path: "$.items", Operators: map[string]any{"unique": false, "length": 2}},
		{Path: "$.nested", Operators: map[string]any{"length": 2}},
	}}}
	out, err := EvaluateAssertions(expect, HTTPResponse{StatusCode: 200}, nil, body, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Passed {
		t.Fatalf("expected utility operators to pass: %#v", out.Evaluations)
	}
}

func TestBinarySHA256Assertion(t *testing.T) {
	content := []byte("veriflow")
	expect := &ExpectationSpec{Binary: &BinaryExpectation{Operators: map[string]any{
		"sha256": "bca5add5473b6000ba18f06f3f0fdcf8fb7cf6abc4b47a6a3e3332c6ed2a775f",
		"length": 8,
	}}}
	out, err := EvaluateAssertions(expect, HTTPResponse{Body: content}, nil, nil, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Passed {
		t.Fatalf("expected binary assertions to pass: %#v", out.Evaluations)
	}
}

func TestFailedORIncludesBranchDiagnostics(t *testing.T) {
	expect := &ExpectationSpec{Body: &AssertionClause{Or: []AssertionClause{
		{Path: "$.status", Operators: map[string]any{"equals": "Open"}},
		{Path: "$.status", Operators: map[string]any{"equals": "Pending"}},
	}}}
	out, err := EvaluateAssertions(expect, HTTPResponse{}, nil, map[string]any{"status": "Closed"}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Passed {
		t.Fatal("expected OR assertion to fail")
	}
	seen := map[string]bool{}
	for _, evaluation := range out.Evaluations {
		seen[evaluation.Target] = true
	}
	if !seen["or[0].$.status"] || !seen["or[1].$.status"] {
		t.Fatalf("expected both failed OR branches in diagnostics, got %#v", out.Evaluations)
	}
}

func TestOptionalCookieJarPersistsCookiesAcrossRequests(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "abc123", Path: "/"})
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/me", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("session")
		if err != nil {
			http.Error(w, "missing session", http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, cookie.Value)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	client, err := NewHTTPClient(HTTPClientConfig{EnableCookieJar: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := SendHTTP(context.Background(), client, PreparedRequest{Method: http.MethodPost, URL: server.URL + "/login"}, 0, nil); err != nil {
		t.Fatal(err)
	}
	response, err := SendHTTP(context.Background(), client, PreparedRequest{Method: http.MethodGet, URL: server.URL + "/me"}, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || response.Text() != "abc123" {
		t.Fatalf("expected persisted session cookie, got status=%d body=%q", response.StatusCode, response.Text())
	}
}

func TestSelectionExclusionsTakePrecedence(t *testing.T) {
	test := TestSpec{ID: "smoke", Name: "Smoke", Tags: []string{"pipeline", "fast"}}
	opts := DefaultRunnerOptions()
	opts.TestIDs = map[string]bool{"smoke": true}
	opts.ExcludeTestIDs = map[string]bool{"smoke": true}
	if opts.testMatches(test) {
		t.Fatal("excluded test id must win over positive selection")
	}

	opts = DefaultRunnerOptions()
	opts.Tags = map[string]bool{"pipeline": true}
	opts.ExcludeTags = map[string]bool{"fast": true}
	if opts.testMatches(test) {
		t.Fatal("excluded tag must win over positive tag selection")
	}
}

func TestSpecFileSizeLimit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.yml")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(DefaultMaxSpecBytes + 1); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	_ = f.Close()
	if _, err := loadDocument(path); err == nil || !strings.Contains(err.Error(), "exceeds maximum size") {
		t.Fatalf("expected size-limit error, got %v", err)
	}
}

func TestYAMLNestingLimit(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxYAMLDepth+2; i++ {
		b.WriteString(strings.Repeat("  ", i))
		fmt.Fprintf(&b, "level%d:\n", i)
	}
	b.WriteString(strings.Repeat("  ", maxYAMLDepth+2))
	b.WriteString("value: true\n")
	if _, _, err := parseYAML([]byte(b.String())); err == nil || !strings.Contains(strings.ToLower(err.Error()), "depth") {
		t.Fatalf("expected nesting-limit error, got %v", err)
	}
}

func TestRequestOutputRequiredAndSensitiveAreInheritedByExtraction(t *testing.T) {
	requestDef := &RequestDefinitionSpec{Outputs: map[string]OutputDefinition{
		"token": {Path: "$.token", Required: true, Sensitive: true},
	}}
	specs := map[string]ExtractionSpec{
		"token": {FromDefinition: "token", Scope: TestScope},
	}
	missing, err := ExtractValues(specs, requestDef, HTTPResponse{JSON: map[string]any{}}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if missing.OK || len(missing.Results) != 1 || missing.Results[0].Passed {
		t.Fatalf("required output should make extraction fail: %#v", missing)
	}

	found, err := ExtractValues(specs, requestDef, HTTPResponse{JSON: map[string]any{"token": "secret"}}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !found.OK || len(found.Results) != 1 || !found.Results[0].Sensitive || found.Results[0].Value != "secret" {
		t.Fatalf("expected output sensitivity to be inherited: %#v", found)
	}
}

func TestValidationCatchesUnknownFromDefinitionOutput(t *testing.T) {
	root := t.TempDir()
	for _, dir := range []string{"suites", "requests/users"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	request := `formatVersion: "1.0"
kind: requestDefinition
id: users/get
request:
  method: GET
  url: "http://example.invalid"
outputs:
  id:
    path: "$.id"
`
	suite := `formatVersion: "1.0"
kind: testSuite
id: bad-output
info:
  name: Bad output
tests:
  - id: one
    steps:
      - id: get
        use: "users/get"
        extract:
          value:
            fromDefinition: missing
`
	if err := os.WriteFile(filepath.Join(root, "requests/users/get.yml"), []byte(request), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "suites/bad.yml"), []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := New().Validate(filepath.Join(root, "suites/bad.yml"), "", root)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, diagnostic := range result.Diagnostics {
		if DiagnosticMatches(diagnostic, "unknown-request-output") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected unknown-request-output diagnostic, got %#v", result.Diagnostics)
	}
}

func TestHeaderExistsAssertionDistinguishesMissingFromEmpty(t *testing.T) {
	expect := &ExpectationSpec{Headers: map[string]HeaderExpectation{
		"X-Missing": {Operators: map[string]any{"exists": false}},
		"X-Empty":   {Operators: map[string]any{"exists": true, "equals": ""}},
	}}
	out, err := EvaluateAssertions(expect, HTTPResponse{Headers: map[string]string{"X-Empty": ""}}, nil, nil, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if !out.Passed {
		t.Fatalf("expected header existence checks to pass: %#v", out.Evaluations)
	}

	expect.Headers["X-Missing"] = HeaderExpectation{Operators: map[string]any{"exists": true}}
	out, err = EvaluateAssertions(expect, HTTPResponse{Headers: map[string]string{"X-Empty": ""}}, nil, nil, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out.Passed {
		t.Fatal("missing header must fail exists:true")
	}
}

func TestValidationRejectsPathlessAssertionOperators(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "suites"), 0o755); err != nil {
		t.Fatal(err)
	}
	suite := `formatVersion: "1.0"
kind: testSuite
id: pathless
info:
  name: Pathless
tests:
  - id: one
    steps:
      - id: request
        request:
          method: GET
          url: "http://example.invalid"
        expect:
          body:
            equals: true
`
	path := filepath.Join(root, "suites", "pathless.yml")
	if err := os.WriteFile(path, []byte(suite), 0o644); err != nil {
		t.Fatal(err)
	}
	result, err := New().Validate(path, "", root)
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range result.Diagnostics {
		if DiagnosticMatches(diagnostic, "assertion-path-required") {
			return
		}
	}
	t.Fatalf("expected assertion-path-required diagnostic, got %#v", result.Diagnostics)
}

func TestYAMLAcceptsUTF8BOMAndRejectsInvalidUTF8(t *testing.T) {
	valid := append([]byte{0xef, 0xbb, 0xbf}, []byte("formatVersion: \"1.0\"\nkind: environment\nname: bom\nvariables: {}\n")...)
	raw, _, err := parseYAML(valid)
	if err != nil {
		t.Fatal(err)
	}
	if raw["kind"] != "environment" {
		t.Fatalf("unexpected BOM parse result: %#v", raw)
	}
	if _, _, err := parseYAML([]byte{0xff, 0xfe, 0xfd}); err == nil || !strings.Contains(err.Error(), "valid UTF-8") {
		t.Fatalf("expected invalid UTF-8 error, got %v", err)
	}
}

func TestYAMLRejectsDuplicateMappingKeys(t *testing.T) {
	_, _, err := parseYAML([]byte("kind: environment\nkind: testSuite\n"))
	if err == nil || !strings.Contains(err.Error(), "duplicate mapping key") {
		t.Fatalf("expected duplicate-key error, got %v", err)
	}
}

func TestSuiteHooksLifecycleAndScopes(t *testing.T) {
	var mu sync.Mutex
	order := []string{}
	record := func(name string) {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, name)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/setup":
			record("beforeAll")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, `{"id":"hook-resource"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/before":
			if got := r.URL.Query().Get("id"); got != "hook-resource" {
				t.Errorf("beforeEach did not receive suite variable: %q", got)
			}
			record("beforeEach")
			_, _ = io.WriteString(w, `{"ok":true}`)
		case r.Method == http.MethodGet && r.URL.Path == "/resource/hook-resource":
			record("test")
			_, _ = io.WriteString(w, `{"id":"hook-resource"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/after":
			if got := r.URL.Query().Get("id"); got != "hook-resource" {
				t.Errorf("afterEach did not receive suite variable: %q", got)
			}
			record("afterEach")
			_, _ = io.WriteString(w, `{"ok":true}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/setup/hook-resource":
			record("afterAll")
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "suites"))
	suitePath := filepath.Join(root, "suites", "hooks.yml")
	mustWrite(t, suitePath, `formatVersion: "1.0"
kind: testSuite
id: hooks
info:
  name: Hooks
hooks:
  beforeAll:
    - id: setup
      request:
        method: POST
        url: "`+server.URL+`/setup"
      expect:
        status: 201
      extract:
        suiteResourceId:
          from: "$.id"
          scope: suite
          required: true
  beforeEach:
    - id: before
      request:
        method: GET
        url: "`+server.URL+`/before"
        query:
          id: "{{suiteResourceId}}"
      expect:
        status: 200
  afterEach:
    - id: after
      request:
        method: GET
        url: "`+server.URL+`/after"
        query:
          id: "{{suiteResourceId}}"
      expect:
        status: 200
  afterAll:
    - id: cleanup
      request:
        method: DELETE
        url: "`+server.URL+`/setup/{{suiteResourceId}}"
      expect:
        status: 204
tests:
  - id: consume
    steps:
      - id: get
        request:
          method: GET
          url: "`+server.URL+`/resource/{{suiteResourceId}}"
        expect:
          status: 200
          body:
            path: "$.id"
            equals: "{{suiteResourceId}}"
`)

	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	result, err := New().Run(context.Background(), suitePath, "", &SummaryReporter{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Passed || len(result.BeforeAll) != 1 || len(result.AfterAll) != 1 || len(result.Tests) != 1 {
		t.Fatalf("unexpected hook result: %#v", result)
	}
	if len(result.Tests[0].BeforeEach) != 1 || len(result.Tests[0].AfterEach) != 1 || len(result.Tests[0].Steps) != 1 {
		t.Fatalf("test hook results missing: %#v", result.Tests[0])
	}
	mu.Lock()
	gotOrder := append([]string{}, order...)
	mu.Unlock()
	wantOrder := []string{"beforeAll", "beforeEach", "test", "afterEach", "afterAll"}
	if !reflect.DeepEqual(gotOrder, wantOrder) {
		t.Fatalf("hook order = %#v want %#v", gotOrder, wantOrder)
	}
}

func TestBeforeEachFailureSkipsBodyButRunsAfterEach(t *testing.T) {
	var mu sync.Mutex
	bodyCalls, cleanupCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/before":
			w.WriteHeader(http.StatusInternalServerError)
		case "/body":
			bodyCalls++
			w.WriteHeader(http.StatusOK)
		case "/cleanup":
			cleanupCalls++
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "suites"))
	path := filepath.Join(root, "suites", "hook-failure.yml")
	mustWrite(t, path, `formatVersion: "1.0"
kind: testSuite
id: hook-failure
info:
  name: Hook failure
hooks:
  beforeEach:
    - id: setup
      request:
        method: GET
        url: "`+server.URL+`/before"
      expect:
        status: 200
  afterEach:
    - id: cleanup
      request:
        method: DELETE
        url: "`+server.URL+`/cleanup"
      expect:
        status: 204
tests:
  - id: body
    steps:
      - id: body-call
        request:
          method: GET
          url: "`+server.URL+`/body"
        expect:
          status: 200
`)
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	result, err := New().Run(context.Background(), path, "", &SummaryReporter{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Failed || len(result.Tests) != 1 || result.Tests[0].Status != Failed {
		t.Fatalf("hook failure must fail test and suite: %#v", result)
	}
	if len(result.Tests[0].BeforeEach) != 1 || result.Tests[0].BeforeEach[0].Status != Failed {
		t.Fatalf("beforeEach failure missing: %#v", result.Tests[0])
	}
	if len(result.Tests[0].Steps) != 0 {
		t.Fatalf("test body must not run after beforeEach failure: %#v", result.Tests[0].Steps)
	}
	if len(result.Tests[0].AfterEach) != 1 || result.Tests[0].AfterEach[0].Status != Passed {
		t.Fatalf("afterEach must still run: %#v", result.Tests[0].AfterEach)
	}
	mu.Lock()
	defer mu.Unlock()
	if bodyCalls != 0 || cleanupCalls != 1 {
		t.Fatalf("bodyCalls=%d cleanupCalls=%d", bodyCalls, cleanupCalls)
	}
}

func TestBeforeAllFailureRunsAfterAllAndAppearsInJUnit(t *testing.T) {
	var mu sync.Mutex
	bodyCalls, cleanupCalls := 0, 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch r.URL.Path {
		case "/setup":
			w.WriteHeader(http.StatusInternalServerError)
		case "/body":
			bodyCalls++
			w.WriteHeader(http.StatusOK)
		case "/cleanup":
			cleanupCalls++
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, "suites"))
	path := filepath.Join(root, "suites", "suite-hook-failure.yml")
	mustWrite(t, path, `formatVersion: "1.0"
kind: testSuite
id: suite-hook-failure
info:
  name: Suite hook failure
hooks:
  beforeAll:
    - id: setup
      request:
        method: POST
        url: "`+server.URL+`/setup"
      expect:
        status: 201
  afterAll:
    - id: cleanup
      request:
        method: DELETE
        url: "`+server.URL+`/cleanup"
      expect:
        status: 204
tests:
  - id: body
    steps:
      - id: body-call
        request:
          method: GET
          url: "`+server.URL+`/body"
        expect:
          status: 200
`)
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	result, err := New().Run(context.Background(), path, "", &SummaryReporter{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != Failed || len(result.BeforeAll) != 1 || result.BeforeAll[0].Status != Failed || len(result.Tests) != 0 {
		t.Fatalf("unexpected beforeAll failure result: %#v", result)
	}
	if len(result.AfterAll) != 1 || result.AfterAll[0].Status != Passed {
		t.Fatalf("afterAll must run after beforeAll failure: %#v", result.AfterAll)
	}
	mu.Lock()
	if bodyCalls != 0 || cleanupCalls != 1 {
		mu.Unlock()
		t.Fatalf("bodyCalls=%d cleanupCalls=%d", bodyCalls, cleanupCalls)
	}
	mu.Unlock()

	junitPath := filepath.Join(root, "junit.xml")
	if err := WriteJUnit(junitPath, []SuiteResult{result}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(junitPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `name="beforeAll"`) || !strings.Contains(text, `type="veriflow.hook"`) {
		t.Fatalf("suite hook failure missing from JUnit:\n%s", text)
	}
}
