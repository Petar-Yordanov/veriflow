package engine

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

func TestSpecFormatVersionIsExactContract(t *testing.T) {
	for _, tc := range []struct {
		version string
		ok      bool
	}{
		{SpecFormatVersion, true},
		{"1.1", false},
		{"1.foo", false},
		{"1.999", false},
		{"2.0", false},
	} {
		raw := map[string]any{"formatVersion": tc.version, "kind": string(TestSuiteKind), "tests": []any{}}
		_, err := parseTypedDocument("suite.yml", raw)
		if tc.ok && err != nil {
			t.Fatalf("formatVersion %q unexpectedly rejected: %v", tc.version, err)
		}
		if !tc.ok {
			if err == nil {
				t.Fatalf("future/unknown formatVersion %q must be rejected", tc.version)
			}
			d, ok := DiagnosticFromError(err, "suite.yml")
			if !ok || d.Code != "VF1010" || d.Name != "unsupported-format-version" {
				t.Fatalf("formatVersion %q diagnostic=%#v err=%v", tc.version, d, err)
			}
		}
	}
}

func TestRepeatEveryMeasuredIterationMustPass(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		status := http.StatusOK
		if calls == 2 {
			status = http.StatusInternalServerError
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	root := t.TempDir()
	path := filepath.Join(root, "suites", "repeat-every-iteration.yml")
	mustWrite(t, path, `formatVersion: "1.0"
kind: testSuite
id: repeat-contract
info:
  name: Repeat contract
tests:
  - id: every-response
    steps:
      - id: call
        repeat:
          count: 3
        request:
          method: GET
          url: "`+server.URL+`"
        expect:
          status: 200
`)
	opts := DefaultRunnerOptions()
	opts.ProjectRoot = root
	result, err := New().Run(context.Background(), path, "", &SummaryReporter{}, opts)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("measured calls=%d want 3", calls)
	}
	if result.Status != Failed || len(result.Tests) != 1 || len(result.Tests[0].Steps) != 1 {
		t.Fatalf("repeat must fail when any measured iteration fails: %#v", result)
	}
	step := result.Tests[0].Steps[0]
	foundIterationFailure := false
	for _, a := range step.Assertions {
		if !a.Passed && strings.HasPrefix(a.Target, "iteration[1].") {
			foundIterationFailure = true
		}
	}
	if !foundIterationFailure {
		t.Fatalf("failed measured iteration must remain visible in assertions: %#v", step.Assertions)
	}
}

func TestTimingSummaryTotalIsSumNotMaximum(t *testing.T) {
	got := timingSummary([]float64{10, 20, 30})
	want := map[string]float64{"totalMs": 60, "avgMs": 20, "p95Ms": 30, "maxMs": 30}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("timing summary=%#v want %#v", got, want)
	}
}

func TestHTTPTransportPhaseTimeoutsAreExplicitAndDefaultDisabled(t *testing.T) {
	client, err := NewHTTPClient(HTTPClientConfig{})
	if err != nil {
		t.Fatal(err)
	}
	tr, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("transport=%T", client.Transport)
	}
	if tr.TLSHandshakeTimeout != 0 || tr.ResponseHeaderTimeout != 0 {
		t.Fatalf("phase timeouts must not silently override request/test/suite deadlines: TLS=%s headers=%s", tr.TLSHandshakeTimeout, tr.ResponseHeaderTimeout)
	}

	client, err = NewHTTPClient(HTTPClientConfig{ConnectTimeoutMS: 123, TLSHandshakeTimeoutMS: 456, ResponseHeaderTimeoutMS: 789})
	if err != nil {
		t.Fatal(err)
	}
	tr = client.Transport.(*http.Transport)
	if tr.TLSHandshakeTimeout.Milliseconds() != 456 || tr.ResponseHeaderTimeout.Milliseconds() != 789 {
		t.Fatalf("configured transport timeouts not applied: TLS=%s headers=%s", tr.TLSHandshakeTimeout, tr.ResponseHeaderTimeout)
	}
}

func TestSelectorDialectCompatibilityContract(t *testing.T) {
	doc := map[string]any{
		"minimumAge": 21,
		"dotted.key": map[string]any{"value": "quoted"},
		"object":     map[string]any{"b": 2, "a": 1},
		"users": []any{
			map[string]any{"id": 1, "name": "Alice", "age": 30, "active": true, "nullable": nil, "roles": []any{"admin", "user"}},
			map[string]any{"id": 2, "name": "Bob", "age": 19, "active": true, "roles": []any{"user"}},
			map[string]any{"id": 3, "name": "Carol", "age": 42, "active": false, "roles": []any{"auditor"}},
		},
	}

	cases := []struct {
		selector string
		missing  bool
		want     any
	}{
		{`$`, false, doc},
		{`$.users[0].name`, false, "Alice"},
		{`$['dotted.key'].value`, false, "quoted"},
		{`$.object.*`, false, []any{1, 2}}, // map traversal is intentionally sorted by key
		{`$.users[-1].id`, false, 3},
		{`$.users[0,2].id`, false, []any{1, 3}},
		{`$.users[0:3:2].id`, false, []any{1, 3}},
		{`$.users[::-1].id`, false, []any{3, 2, 1}},
		{`$.users[*].name`, false, []any{"Alice", "Bob", "Carol"}},
		{`$.users[?(@.age >= 21)].name`, false, []any{"Alice", "Carol"}},
		{`$.users[?(@.age >= $.minimumAge && @.active == true)].name`, false, "Alice"},
		{`$.users[?(@.name =~ /^a/i)].id`, false, 1},
		{`$.users[?(@.nullable == null)].id`, false, 1},
		{`$.users[?(@.missing)].id`, true, nil},
		{`$..id`, false, []any{1, 2, 3}},
		{`$.missing`, true, nil},
	}

	for _, tc := range cases {
		t.Run(tc.selector, func(t *testing.T) {
			got, err := Select(doc, tc.selector)
			if err != nil {
				t.Fatalf("selector %q: %v", tc.selector, err)
			}
			if got.Missing != tc.missing || !reflect.DeepEqual(got.Value, tc.want) {
				t.Fatalf("selector %q got=%#v missing=%v want=%#v missing=%v", tc.selector, got.Value, got.Missing, tc.want, tc.missing)
			}
		})
	}

	for _, selector := range []string{
		`$.users[?(@.age == garbage)]`,
		`$.users[?(@.name =~ /abc/z)]`,
		`$.users[0:2:0]`,
		`$..[0]`,
		`$.users[?(`,
	} {
		if IsSupportedJSONPath(selector) {
			t.Fatalf("malformed selector accepted: %s", selector)
		}
	}
}

func TestAllLiteralDiagnosticNamesAreRegistered(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate engine package")
	}
	dir := filepath.Dir(file)
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	pkg := pkgs["engine"]
	if pkg == nil {
		t.Fatal("engine package not parsed")
	}
	checked := 0
	for filename, f := range pkg.Files {
		if strings.HasSuffix(filename, "_test.go") {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			ident, ok := call.Fun.(*ast.Ident)
			if !ok {
				return true
			}
			switch ident.Name {
			case "NewDiagnostic", "runtimeNamedError", "NewRuntimeDiagnostic", "userLoadError":
			default:
				return true
			}
			lit, ok := call.Args[0].(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true // dynamic names are validated at their defining boundary
			}
			name, err := strconv.Unquote(lit.Value)
			if err != nil {
				t.Fatalf("%s: diagnostic name literal %s: %v", filename, lit.Value, err)
			}
			checked++
			if !IsKnownDiagnosticName(name) {
				pos := fset.Position(lit.Pos())
				t.Errorf("unregistered diagnostic name %q at %s:%d", name, filepath.Base(pos.Filename), pos.Line)
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("diagnostic registry test did not inspect any diagnostic calls")
	}

	seenCodes := map[string]string{}
	for name, code := range DiagnosticRegistrySnapshot() {
		if prior, exists := seenCodes[code]; exists && prior != name {
			t.Errorf("diagnostic code %s is assigned to both %q and %q", code, prior, name)
		}
		seenCodes[code] = name
	}
}

func TestExpandedCaseIdentitiesCannotCollide(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "suites", "collision.yml")
	mustWrite(t, path, `formatVersion: "1.0"
kind: testSuite
id: collision
info:
  name: Collision

tests:
  - id: authorization
    cases:
      admin:
        variables: {}
    steps:
      - id: wait
        wait:
          forMs: 1

  - id: authorization[admin]
    steps:
      - id: wait
        wait:
          forMs: 1
`)
	result, err := New().Validate(path, "", root)
	if err != nil {
		t.Fatal(err)
	}
	seenInvalidBase := false
	seenExpandedCollision := false
	for _, d := range result.Diagnostics {
		if d.Name == "invalid-test-id" && d.Code == "VF1209" {
			seenInvalidBase = true
		}
		if d.Name == "duplicate-expanded-test-id" && d.Code == "VF1216" {
			seenExpandedCollision = true
		}
	}
	if !seenInvalidBase || !seenExpandedCollision {
		t.Fatalf("expected invalid base id and expanded identity collision diagnostics: %#v", result.Diagnostics)
	}
}

func TestSuiteInfoIDIsNotPartOfThe10Schema(t *testing.T) {
	schema := SpecJSONSchema()
	defs := asMap(schema["$defs"])
	suite := asMap(defs["testSuite"])
	properties := asMap(suite["properties"])
	info := asMap(properties["info"])
	infoProperties := asMap(info["properties"])
	if _, exists := infoProperties["id"]; exists {
		t.Fatal("suite info.id must not be part of the 1.0 contract; suite id is top-level only")
	}
	if _, exists := properties["id"]; !exists {
		t.Fatal("top-level suite id must remain in the 1.0 schema")
	}
}

func TestReportAndEventSchemaVersionsAreIndependentContracts(t *testing.T) {
	if ReportSchemaVersion != "1.0" || EventSchemaVersion != "1.0" {
		t.Fatalf("unexpected 1.0 machine schema versions: report=%q event=%q", ReportSchemaVersion, EventSchemaVersion)
	}
	event := NewEvent("contract.test", map[string]any{"ok": true})
	if event.SchemaVersion != EventSchemaVersion {
		t.Fatalf("event schemaVersion=%q want %q", event.SchemaVersion, EventSchemaVersion)
	}
	report := BuildRunReport([]SuiteResult{{SchemaVersion: ReportSchemaVersion, Status: Passed, StatusCode: 0}}, nil)
	if report.SchemaVersion != ReportSchemaVersion || report.Result.SchemaVersion != ReportSchemaVersion {
		t.Fatalf("report schema contract not propagated: %#v", report)
	}
}

func TestSourceVersionMatchesRootVersionFile(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate engine package")
	}
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	b, err := os.ReadFile(filepath.Join(root, "VERSION"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(b)); got != EngineVersion {
		t.Fatalf("VERSION=%q EngineVersion=%q; update them together", got, EngineVersion)
	}
}

func TestRuntimeHTTPFailureClassificationContract(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code string
	}{
		{"cancelled", context.Canceled, "VF3005"},
		{"deadline", context.DeadlineExceeded, "VF3001"},
		{"too-large", &ResponseTooLargeError{Limit: 10}, "VF3004"},
		{"network", errors.New("connection reset"), "VF3002"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := RuntimeDiagnosticFromError(runtimeHTTPError(tc.err))
			if d.Code != tc.code {
				t.Fatalf("runtime HTTP diagnostic=%#v want code %s", d, tc.code)
			}
		})
	}
	if runtimeHTTPError(nil) != nil {
		t.Fatal("nil HTTP error must remain nil")
	}
}

func TestAdHocVariableParsingContract(t *testing.T) {
	got, err := ParseAdHocVariables([]string{
		"count=42",
		"enabled=true",
		"nested.name=alpha",
		`payload={"id":7,"items":[1,2]}`,
		"plain=not-json",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got["count"] != int64(42) || got["enabled"] != true || got["plain"] != "not-json" {
		t.Fatalf("unexpected scalar CLI variables: %#v", got)
	}
	if asMap(got["nested"])["name"] != "alpha" || asInt(asMap(got["payload"])["id"]) != 7 {
		t.Fatalf("unexpected nested/object CLI variables: %#v", got)
	}
	if _, err := ParseAdHocVariables([]string{"broken"}); err == nil {
		t.Fatal("missing '=' must be rejected")
	}
	if _, err := ParseAdHocVariables([]string{"a=1", "a.b=2"}); err == nil {
		t.Fatal("conflicting dotted variable path must be rejected")
	}
}
