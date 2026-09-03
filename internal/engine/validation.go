package engine

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var testCaseIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
var testIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

var allowedFields = map[SpecKind]map[string]bool{
	RequestDefinitionKind: setOf("formatVersion", "kind", "id", "name", "description", "inputs", "request", "outputs"),
	TestSuiteKind:         setOf("formatVersion", "kind", "info", "timeoutMs", "globals", "defaults", "hooks", "tests", "id", "name", "description"),
	EnvironmentKind:       setOf("formatVersion", "kind", "name", "extends", "variables"),
}

func setOf(items ...string) map[string]bool {
	m := map[string]bool{}
	for _, x := range items {
		m[x] = true
	}
	return m
}

func ValidateBundle(bundle LoadedBundle) ValidationResult {
	d := append([]Diagnostic{}, bundle.ReferenceDiagnostics...)
	d = append(d, validateDocument(bundle.Suite)...)
	if bundle.Environment != nil {
		d = append(d, validateDocument(*bundle.Environment)...)
	}
	for _, doc := range bundle.ReferencedRequests {
		d = append(d, validateDocument(doc)...)
	}
	d = append(d, validateCrossFile(bundle)...)
	d = append(d, validateBundleFiles(bundle)...)
	d = dedupeDiagnostics(d)
	sortDiagnostics(d)
	return ValidationResult{Diagnostics: d}
}
func validateDocument(doc LoadedDocument) []Diagnostic {
	d := []Diagnostic{}
	kind := SpecKind(asString(doc.Raw["kind"]))
	allowed := allowedFields[kind]
	if allowed != nil {
		keys := []string{}
		for k := range doc.Raw {
			if !allowed[k] {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			d = append(d, diag(doc, "$."+k, "unknown-field", fmt.Sprintf("Unknown field '%s' for kind '%s'", k, kind)))
		}
	}
	d = append(d, validateRawTypes(doc)...)
	d = append(d, validateNestedShape(doc)...)
	switch t := doc.Typed.(type) {
	case TestSuiteSpec:
		d = append(d, validateSuite(doc, t)...)
	case RequestDefinitionSpec:
		d = append(d, validateRequestDefinition(doc, t)...)
	case EnvironmentSpec:
		if t.Variables == nil {
			d = append(d, diag(doc, "$.variables", "environment-variables", "Environment variables must be a mapping"))
		}
	}
	return d
}
func validateRawTypes(doc LoadedDocument) []Diagnostic {
	d := []Diagnostic{}
	expectMap := func(parent map[string]any, key, path string) {
		if v, exists := parent[key]; exists && v != nil {
			if _, ok := v.(map[string]any); !ok {
				d = append(d, diag(doc, path, "invalid-type", fmt.Sprintf("%s must be a mapping", path)))
			}
		}
	}
	expectList := func(parent map[string]any, key, path string) {
		if v, exists := parent[key]; exists && v != nil {
			if _, ok := v.([]any); !ok {
				d = append(d, diag(doc, path, "invalid-type", fmt.Sprintf("%s must be a sequence", path)))
			}
		}
	}
	expectString := func(parent map[string]any, key, path string) {
		if v, exists := parent[key]; exists && v != nil {
			if _, ok := v.(string); !ok {
				d = append(d, diag(doc, path, "invalid-type", fmt.Sprintf("%s must be a string", path)))
			}
		}
	}

	expectString(doc.Raw, "formatVersion", "$.formatVersion")
	expectString(doc.Raw, "kind", "$.kind")
	kind := SpecKind(asString(doc.Raw["kind"]))
	switch kind {
	case RequestDefinitionKind:
		expectMap(doc.Raw, "request", "$.request")
		expectMap(doc.Raw, "inputs", "$.inputs")
		expectMap(doc.Raw, "outputs", "$.outputs")
		if req := asMap(doc.Raw["request"]); req != nil {
			d = append(d, validateRawRequestTypes(doc, "$.request", req)...)
		}
	case EnvironmentKind:
		expectString(doc.Raw, "name", "$.name")
		expectString(doc.Raw, "extends", "$.extends")
		expectMap(doc.Raw, "variables", "$.variables")
	case TestSuiteKind:
		expectMap(doc.Raw, "info", "$.info")
		expectMap(doc.Raw, "globals", "$.globals")
		expectMap(doc.Raw, "defaults", "$.defaults")
		expectMap(doc.Raw, "hooks", "$.hooks")
		if v, exists := doc.Raw["timeoutMs"]; exists && v != nil && !isIntegerValue(v) {
			d = append(d, diag(doc, "$.timeoutMs", "invalid-type", "timeoutMs must be an integer"))
		}
		expectList(doc.Raw, "tests", "$.tests")
		if hooks := asMap(doc.Raw["hooks"]); hooks != nil {
			for _, hook := range []string{"beforeAll", "afterAll", "beforeEach", "afterEach"} {
				expectList(hooks, hook, "$.hooks."+hook)
				for si, sv := range asSlice(hooks[hook]) {
					sm, ok := sv.(map[string]any)
					sp := fmt.Sprintf("$.hooks.%s[%d]", hook, si)
					if !ok {
						d = append(d, diag(doc, sp, "invalid-type", "hook step entry must be a mapping"))
						continue
					}
					for _, key := range []string{"variables", "with", "request", "extend", "overrides", "wait", "expect", "extract", "retry", "repeat", "log", "artifacts"} {
						expectMap(sm, key, sp+"."+key)
					}
					if req := asMap(sm["request"]); req != nil {
						d = append(d, validateRawRequestTypes(doc, sp+".request", req)...)
					}
				}
			}
		}
		if globals := asMap(doc.Raw["globals"]); globals != nil {
			expectMap(globals, "variables", "$.globals.variables")
		}
		for ti, tv := range asSlice(doc.Raw["tests"]) {
			tm, ok := tv.(map[string]any)
			path := fmt.Sprintf("$.tests[%d]", ti)
			if !ok {
				d = append(d, diag(doc, path, "invalid-type", "test entry must be a mapping"))
				continue
			}
			expectString(tm, "id", path+".id")
			expectString(tm, "name", path+".name")
			expectMap(tm, "variables", path+".variables")
			expectMap(tm, "cases", path+".cases")
			if v, exists := tm["timeoutMs"]; exists && v != nil && !isIntegerValue(v) {
				d = append(d, diag(doc, path+".timeoutMs", "invalid-type", "timeoutMs must be an integer"))
			}
			for caseID, cv := range asMap(tm["cases"]) {
				cp := path + ".cases." + caseID
				cm, ok := cv.(map[string]any)
				if !ok {
					d = append(d, diag(doc, cp, "invalid-type", "test case must be a mapping"))
					continue
				}
				expectString(cm, "name", cp+".name")
				expectMap(cm, "variables", cp+".variables")
				if v, exists := cm["skip"]; exists && v != nil {
					if _, ok := v.(bool); !ok {
						d = append(d, diag(doc, cp+".skip", "invalid-type", "skip must be a boolean"))
					}
				}
			}
			expectList(tm, "tags", path+".tags")
			expectList(tm, "steps", path+".steps")
			for si, sv := range asSlice(tm["steps"]) {
				sm, ok := sv.(map[string]any)
				sp := fmt.Sprintf("%s.steps[%d]", path, si)
				if !ok {
					d = append(d, diag(doc, sp, "invalid-type", "step entry must be a mapping"))
					continue
				}
				for _, key := range []string{"variables", "with", "request", "extend", "overrides", "wait", "expect", "extract", "retry", "repeat", "log", "artifacts"} {
					expectMap(sm, key, sp+"."+key)
				}
				if req := asMap(sm["request"]); req != nil {
					d = append(d, validateRawRequestTypes(doc, sp+".request", req)...)
				}
			}
		}
	}
	return d
}

func validateRawRequestTypes(doc LoadedDocument, path string, raw map[string]any) []Diagnostic {
	d := []Diagnostic{}
	for _, key := range []string{"pathParams", "pathParamEncoding", "query", "headers", "cookies", "auth", "form", "multipart"} {
		if v, exists := raw[key]; exists && v != nil {
			if _, ok := v.(map[string]any); !ok {
				d = append(d, diag(doc, path+"."+key, "invalid-type", fmt.Sprintf("%s.%s must be a mapping", path, key)))
			}
		}
	}
	for _, key := range []string{"method", "url", "baseUrl", "path", "bodyRaw", "bodyFile", "bodyFileMode"} {
		if v, exists := raw[key]; exists && v != nil {
			if _, ok := v.(string); !ok {
				d = append(d, diag(doc, path+"."+key, "invalid-type", fmt.Sprintf("%s.%s must be a string", path, key)))
			}
		}
	}
	if v, exists := raw["followRedirects"]; exists && v != nil {
		if _, ok := v.(bool); !ok {
			d = append(d, diag(doc, path+".followRedirects", "invalid-type", "followRedirects must be a boolean"))
		}
	}
	if v, exists := raw["timeoutMs"]; exists && v != nil && !isIntegerValue(v) {
		d = append(d, diag(doc, path+".timeoutMs", "invalid-type", "timeoutMs must be an integer"))
	}
	return d
}

func isIntegerValue(v any) bool {
	switch x := v.(type) {
	case int, int64:
		return true
	case float64:
		return x == float64(int64(x))
	}
	return false
}

func validateSuite(doc LoadedDocument, s TestSuiteSpec) []Diagnostic {
	d := []Diagnostic{}
	if s.TimeoutMS < 0 {
		d = append(d, diag(doc, "$.timeoutMs", "invalid-timeout", "suite timeoutMs cannot be negative"))
	}
	if s.Defaults.TimeoutMS < 0 {
		d = append(d, diag(doc, "$.defaults.timeoutMs", "invalid-timeout", "defaults.timeoutMs cannot be negative"))
	}
	if s.Defaults.Retry != nil {
		d = append(d, validateRetry(doc, "$.defaults.retry", *s.Defaults.Retry)...)
	}
	for hookName, steps := range map[string][]StepSpec{
		"beforeAll": s.Hooks.BeforeAll, "afterAll": s.Hooks.AfterAll,
		"beforeEach": s.Hooks.BeforeEach, "afterEach": s.Hooks.AfterEach,
	} {
		seen := map[string]bool{}
		for si, st := range steps {
			base := fmt.Sprintf("$.hooks.%s[%d]", hookName, si)
			if st.ID != "" && seen[st.ID] {
				d = append(d, diag(doc, base+".id", "duplicate-step-id", fmt.Sprintf("Duplicate step id '%s' in hook '%s'", st.ID, hookName)))
			}
			if st.ID != "" {
				seen[st.ID] = true
			}
			d = append(d, validateStepAtPath(doc, base, st)...)
		}
	}
	tests := map[string]bool{}
	expandedIDs := map[string]int{}
	for ti, t := range s.Tests {
		if strings.TrimSpace(t.ID) == "" {
			d = append(d, diag(doc, fmt.Sprintf("$.tests[%d].id", ti), "missing-test-id", "Test id is required"))
		} else if !testIDPattern.MatchString(t.ID) {
			d = append(d, diag(doc, fmt.Sprintf("$.tests[%d].id", ti), "invalid-test-id", fmt.Sprintf("test id %q must match %s", t.ID, testIDPattern.String())))
		}
		if tests[t.ID] {
			d = append(d, diag(doc, fmt.Sprintf("$.tests[%d].id", ti), "duplicate-test-id", fmt.Sprintf("Duplicate test id '%s'", t.ID)))
		}
		tests[t.ID] = true
		if t.TimeoutMS < 0 {
			d = append(d, diag(doc, fmt.Sprintf("$.tests[%d].timeoutMs", ti), "invalid-timeout", "test timeoutMs cannot be negative"))
		}
		for caseID := range t.Cases {
			casePath := fmt.Sprintf("$.tests[%d].cases.%s", ti, caseID)
			if !testCaseIDPattern.MatchString(caseID) {
				d = append(d, diag(doc, casePath, "invalid-test-case", fmt.Sprintf("test case id %q must match %s", caseID, testCaseIDPattern.String())))
			}
		}
		for _, expanded := range ExpandTestCases(t) {
			if previous, exists := expandedIDs[expanded.ID]; exists {
				d = append(d, diag(doc, fmt.Sprintf("$.tests[%d].id", ti), "duplicate-expanded-test-id", fmt.Sprintf("runtime test id %q collides with tests[%d]", expanded.ID, previous)))
			} else {
				expandedIDs[expanded.ID] = ti
			}
		}
		steps := map[string]bool{}
		for si, st := range t.Steps {
			base := fmt.Sprintf("$.tests[%d].steps[%d]", ti, si)
			if st.ID != "" && steps[st.ID] {
				d = append(d, diag(doc, base+".id", "duplicate-step-id", fmt.Sprintf("Duplicate step id '%s' in test '%s'", st.ID, t.ID)))
			}
			if st.ID != "" {
				steps[st.ID] = true
			}
			d = append(d, validateStepAtPath(doc, base, st)...)
		}
	}
	return d
}

func validateStepAtPath(doc LoadedDocument, base string, st StepSpec) []Diagnostic {
	d := []Diagnostic{}
	waitOnly := st.Wait != nil && st.Wait.ForMS > 0 && st.Use == "" && st.Request == nil
	if !waitOnly && ((st.Use != "") == (st.Request != nil)) {
		d = append(d, diag(doc, base, "step-shape", "Step must define exactly one of 'use' or 'request', unless it is a wait-only step"))
	}
	if st.Request != nil {
		d = append(d, validateRequest(doc, base+".request", *st.Request)...)
	}
	if st.Expect != nil && st.Expect.Body != nil {
		d = append(d, validateClause(doc, base+".expect.body", *st.Expect.Body)...)
	}
	for name, e := range st.Extract {
		sourceCount := 0
		for _, active := range []bool{e.FromSelector != "", e.FromDefinition != "", e.FromHeader != "", e.FromCookie != "", e.FromTextRegex != "", e.FromStatus} {
			if active {
				sourceCount++
			}
		}
		if sourceCount != 1 {
			d = append(d, diag(doc, base+".extract."+name, "extract-source", fmt.Sprintf("Extraction '%s' must define exactly one extraction source", name)))
		}
		if e.FromSelector != "" && !IsSupportedJSONPath(e.FromSelector) {
			d = append(d, diag(doc, base+".extract."+name+".from", "invalid-selector", fmt.Sprintf("Unsupported JSONPath selector '%s'", e.FromSelector)))
		}
		if e.FromDefinition != "" && st.Use == "" {
			d = append(d, diag(doc, base+".extract."+name+".fromDefinition", "from-definition-without-request-definition", "fromDefinition requires a step that uses a request definition"))
		}
		if e.FromTextRegex != "" && !strings.Contains(e.FromTextRegex, "{{") {
			if _, err := regexp.Compile(e.FromTextRegex); err != nil {
				d = append(d, diag(doc, base+".extract."+name+".fromTextRegex", "invalid-regex", fmt.Sprintf("Invalid extraction regular expression: %v", err)))
			}
		}
		if e.Scope != SuiteScope && e.Scope != TestScope && e.Scope != StepScope {
			d = append(d, diag(doc, base+".extract."+name+".scope", "invalid-extraction-scope", fmt.Sprintf("Unsupported extraction scope %q", e.Scope)))
		}
	}
	if st.TimeoutMS < 0 {
		d = append(d, diag(doc, base+".timeoutMs", "invalid-timeout", "timeoutMs cannot be negative"))
	}
	if st.Wait != nil && (st.Wait.BeforeMS < 0 || st.Wait.AfterMS < 0 || st.Wait.ForMS < 0) {
		d = append(d, diag(doc, base+".wait", "invalid-wait", "wait durations cannot be negative"))
	}
	if st.Retry != nil {
		d = append(d, validateRetry(doc, base+".retry", *st.Retry)...)
	}
	if st.Repeat != nil && (st.Repeat.Count < 1 || st.Repeat.WarmupCount < 0) {
		d = append(d, diag(doc, base+".repeat", "invalid-repeat", "repeat.count must be at least 1 and warmupCount cannot be negative"))
	}
	return d
}
func validateClause(doc LoadedDocument, path string, c AssertionClause) []Diagnostic {
	d := []Diagnostic{}
	if c.Path != "" && !IsSupportedJSONPath(c.Path) {
		d = append(d, diag(doc, path, "invalid-selector", fmt.Sprintf("Unsupported JSONPath selector '%s'", c.Path)))
	}
	for i, ch := range c.And {
		d = append(d, validateClause(doc, fmt.Sprintf("%s.and[%d]", path, i), ch)...)
	}
	for i, ch := range c.Or {
		d = append(d, validateClause(doc, fmt.Sprintf("%s.or[%d]", path, i), ch)...)
	}
	return d
}
func validateRequestDefinition(doc LoadedDocument, r RequestDefinitionSpec) []Diagnostic {
	d := []Diagnostic{}
	if _, exists := doc.Raw["request"]; !exists || doc.Raw["request"] == nil {
		d = append(d, diag(doc, "$.request", "missing-request", "requestDefinition requires request"))
	}
	d = append(d, validateRequest(doc, "$.request", r.Request)...)
	inputNames := make([]string, 0, len(r.Inputs))
	for name := range r.Inputs {
		inputNames = append(inputNames, name)
	}
	sort.Strings(inputNames)
	for _, name := range inputNames {
		in := r.Inputs[name]
		if in.Type != "" && !supportedInputType(in.Type) {
			d = append(d, diag(doc, "$.inputs."+name+".type", "invalid-input-type", fmt.Sprintf("Unsupported input type %q", in.Type)))
		}
		if in.Default != nil && in.Type != "" && !strings.EqualFold(in.Type, "any") && !inputTypeMatches(in.Type, in.Default) {
			d = append(d, diag(doc, "$.inputs."+name+".default", "invalid-input-default", fmt.Sprintf("Default for input %q does not match type %q", name, in.Type)))
		}
	}
	outputNames := make([]string, 0, len(r.Outputs))
	for name := range r.Outputs {
		outputNames = append(outputNames, name)
	}
	sort.Strings(outputNames)
	for _, name := range outputNames {
		o := r.Outputs[name]
		if !IsSupportedJSONPath(o.Path) {
			d = append(d, diag(doc, "$.outputs."+name+".path", "invalid-selector", fmt.Sprintf("Unsupported JSONPath selector '%s'", o.Path)))
		}
	}
	return d
}
func validateRequest(doc LoadedDocument, base string, r RequestSpec) []Diagnostic {
	d := []Diagnostic{}
	modes := 0
	if r.Body != nil {
		modes++
	}
	if r.BodyRaw != "" {
		modes++
	}
	if r.BodyFile != "" {
		modes++
	}
	if len(r.Form) > 0 {
		modes++
	}
	if len(r.Multipart) > 0 {
		modes++
	}
	if modes > 1 {
		d = append(d, diag(doc, base, "conflicting-body-modes", "Request defines conflicting body modes"))
	}
	if r.URL == "" && (r.BaseURL == "" || r.Path == "") {
		d = append(d, diag(doc, base, "request-url-shape", "Request must define 'url' or both 'baseUrl' and 'path'"))
	}
	for k, v := range r.Query {
		if v == nil {
			d = append(d, diag(doc, base+".query."+k, "null-query-value", fmt.Sprintf("Query parameter '%s' cannot be null", k)))
		}
	}
	for k, v := range r.Cookies {
		if v == nil {
			d = append(d, diag(doc, base+".cookies."+k, "null-cookie-value", fmt.Sprintf("Cookie '%s' cannot be null", k)))
		}
	}
	if r.TimeoutMS < 0 {
		d = append(d, diag(doc, base+".timeoutMs", "invalid-timeout", "timeoutMs cannot be negative"))
	}
	if !validHTTPMethod(r.Method) {
		d = append(d, diag(doc, base+".method", "invalid-http-method", fmt.Sprintf("Unsupported HTTP method %q", r.Method)))
	}
	if r.URL != "" && !strings.Contains(r.URL, "{{") {
		if err := validateHTTPURL(r.URL); err != nil {
			d = append(d, diag(doc, base+".url", "invalid-url", err.Error()))
		}
	}
	if r.BaseURL != "" && !strings.Contains(r.BaseURL, "{{") {
		if err := validateHTTPURL(r.BaseURL); err != nil {
			d = append(d, diag(doc, base+".baseUrl", "invalid-url", err.Error()))
		}
	}
	if r.Auth != nil {
		authType := strings.ToLower(strings.TrimSpace(r.Auth.Type))
		switch authType {
		case "basic":
			if r.Auth.Username == "" {
				d = append(d, diag(doc, base+".auth.username", "invalid-auth", "basic auth requires username"))
			}
			if r.Auth.Password == "" {
				d = append(d, diag(doc, base+".auth.password", "invalid-auth", "basic auth requires password"))
			}
		case "bearer":
			if r.Auth.Token == "" {
				d = append(d, diag(doc, base+".auth.token", "invalid-auth", "bearer auth requires token"))
			}
		case "apikey", "api-key":
			if r.Auth.Name == "" {
				d = append(d, diag(doc, base+".auth.name", "invalid-auth", "apiKey auth requires name"))
			}
			if r.Auth.Value == "" {
				d = append(d, diag(doc, base+".auth.value", "invalid-auth", "apiKey auth requires value"))
			}
			where := strings.ToLower(strings.TrimSpace(r.Auth.In))
			if where != "" && where != "header" && where != "query" {
				d = append(d, diag(doc, base+".auth.in", "invalid-auth", "apiKey auth 'in' must be header or query"))
			}
		default:
			d = append(d, diag(doc, base+".auth.type", "invalid-auth", fmt.Sprintf("Unsupported auth type %q", r.Auth.Type)))
		}
	}
	if r.BodyFileMode != "" {
		m := strings.ToLower(r.BodyFileMode)
		if m != "binary" && m != "text" && m != "json" {
			d = append(d, diag(doc, base+".bodyFileMode", "invalid-body-file-mode", "bodyFileMode must be binary, text, or json"))
		}
	}
	return d
}
func validateCrossFile(bundle LoadedBundle) []Diagnostic {
	d := []Diagnostic{}
	suite, ok := bundle.Suite.Typed.(TestSuiteSpec)
	if !ok {
		return d
	}
	for _, ref := range suiteStepRefs(suite) {
		st := ref.Step
		if st.Use == "" {
			continue
		}
		resolved, err := ResolveRequestReference(bundle.ProjectRoot, st.Use)
		path := ref.Path + ".use"
		if err != nil {
			d = append(d, diag(bundle.Suite, path, "invalid-use", err.Error()))
			continue
		}
		doc, ok := bundle.ReferencedRequests[resolved]
		if !ok {
			if _, statErr := os.Stat(resolved); statErr != nil {
				d = append(d, diag(bundle.Suite, path, "missing-use", fmt.Sprintf("Referenced request definition not found: %s", st.Use)))
			} else {
				d = append(d, diag(bundle.Suite, path, "invalid-use", "Referenced request could not be loaded"))
			}
			continue
		}
		if asString(doc.Raw["kind"]) != string(RequestDefinitionKind) {
			d = append(d, diag(bundle.Suite, path, "invalid-use-kind", fmt.Sprintf("Referenced file is not a requestDefinition: %s", st.Use)))
			continue
		}
		rd, ok := doc.Typed.(RequestDefinitionSpec)
		if !ok {
			continue
		}
		extractionNames := make([]string, 0, len(st.Extract))
		for name := range st.Extract {
			extractionNames = append(extractionNames, name)
		}
		sort.Strings(extractionNames)
		for _, name := range extractionNames {
			extraction := st.Extract[name]
			if extraction.FromDefinition == "" {
				continue
			}
			if _, exists := rd.Outputs[extraction.FromDefinition]; !exists {
				d = append(d, diag(bundle.Suite, path, "unknown-request-output", fmt.Sprintf("Unknown output %q for request %q used by extraction %q", extraction.FromDefinition, requestDisplayName(rd), name)))
			}
		}

		withNames := make([]string, 0, len(st.With))
		for name := range st.With {
			withNames = append(withNames, name)
		}
		sort.Strings(withNames)
		for _, name := range withNames {
			input, exists := rd.Inputs[name]
			if !exists {
				d = append(d, diag(bundle.Suite, path, "unknown-input", fmt.Sprintf("Unknown input %q for request %q", name, requestDisplayName(rd))))
				continue
			}
			value := st.With[name]
			if input.Type != "" && !containsPlaceholder(value) && !inputTypeMatches(input.Type, value) {
				d = append(d, diag(bundle.Suite, path, "invalid-input-value", fmt.Sprintf("Input %q for request %q must be %s", name, requestDisplayName(rd), input.Type)))
			}
		}
		inputNames := make([]string, 0, len(rd.Inputs))
		for name := range rd.Inputs {
			inputNames = append(inputNames, name)
		}
		sort.Strings(inputNames)
		for _, name := range inputNames {
			input := rd.Inputs[name]
			if input.Required && input.Default == nil {
				if _, exists := st.With[name]; !exists {
					d = append(d, diag(bundle.Suite, path, "missing-required-input", fmt.Sprintf("Required input %q is missing for request %q", name, requestDisplayName(rd))))
				}
			}
		}
	}
	return d
}

type suiteStepRef struct {
	Path string
	Step StepSpec
}

func suiteStepRefs(suite TestSuiteSpec) []suiteStepRef {
	out := []suiteStepRef{}
	appendSteps := func(base string, steps []StepSpec) {
		for index, step := range steps {
			out = append(out, suiteStepRef{Path: fmt.Sprintf("%s[%d]", base, index), Step: step})
		}
	}
	appendSteps("$.hooks.beforeAll", suite.Hooks.BeforeAll)
	appendSteps("$.hooks.afterAll", suite.Hooks.AfterAll)
	appendSteps("$.hooks.beforeEach", suite.Hooks.BeforeEach)
	appendSteps("$.hooks.afterEach", suite.Hooks.AfterEach)
	for ti, test := range suite.Tests {
		appendSteps(fmt.Sprintf("$.tests[%d].steps", ti), test.Steps)
	}
	return out
}

func diag(doc LoadedDocument, path, name, msg string) Diagnostic {
	loc := DocumentLocation{File: doc.Path, DocumentPath: path}
	if p, ok := doc.SourceMap[path]; ok {
		loc.Line = p[0]
		loc.Column = p[1]
	}
	return NewDiagnostic(name, msg, ErrorSeverity, loc)
}

func validateRetry(doc LoadedDocument, path string, r RetrySpec) []Diagnostic {
	d := []Diagnostic{}
	if r.Count < 0 || r.DelayMS < 0 || r.MaxDelayMS < 0 {
		d = append(d, diag(doc, path, "invalid-retry", "retry count and delays cannot be negative"))
	}
	if r.BackoffMultiplier < 0 {
		d = append(d, diag(doc, path+".backoffMultiplier", "invalid-retry-backoff", "backoffMultiplier cannot be negative"))
	}
	for _, status := range r.When.StatusIn {
		if status < 100 || status > 599 {
			d = append(d, diag(doc, path+".when.statusIn", "invalid-http-status", fmt.Sprintf("Invalid retry HTTP status %d", status)))
		}
	}
	return d
}

func supportedInputType(kind string) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "", "any", "string", "number", "integer", "int", "boolean", "bool", "object", "map", "array", "list", "null":
		return true
	}
	return false
}

func validHTTPMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodConnect, http.MethodOptions, http.MethodTrace:
		return true
	}
	return false
}

func validateNestedShape(doc LoadedDocument) []Diagnostic {
	d := []Diagnostic{}
	rejectUnknown := func(path string, m map[string]any, allowed ...string) {
		set := setOf(allowed...)
		keys := []string{}
		for k := range m {
			if !set[k] {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			d = append(d, diag(doc, path+"."+k, "unknown-field", fmt.Sprintf("Unknown field %q", k)))
		}
	}
	kind := SpecKind(asString(doc.Raw["kind"]))
	if kind == RequestDefinitionKind {
		for name, v := range asMap(doc.Raw["inputs"]) {
			rejectUnknown("$.inputs."+name, asMap(v), "type", "required", "sensitive", "description", "default")
		}
		for name, v := range asMap(doc.Raw["outputs"]) {
			rejectUnknown("$.outputs."+name, asMap(v), "path", "required", "sensitive")
		}
		if m := asMap(doc.Raw["request"]); m != nil {
			rejectUnknown("$.request", m, requestFieldNames()...)
			validateRequestNestedShape(&d, doc, "$.request", m, rejectUnknown)
		}
	}
	if kind == TestSuiteKind {
		if m := asMap(doc.Raw["info"]); m != nil {
			rejectUnknown("$.info", m, "name", "description")
		}
		if m := asMap(doc.Raw["globals"]); m != nil {
			rejectUnknown("$.globals", m, "variables")
		}
		if m := asMap(doc.Raw["defaults"]); m != nil {
			rejectUnknown("$.defaults", m, "timeoutMs", "followRedirects", "headers", "retry")
			if rm := asMap(m["retry"]); rm != nil {
				rejectUnknown("$.defaults.retry", rm, "count", "delayMs", "backoffMultiplier", "maxDelayMs", "when")
				if w := asMap(rm["when"]); w != nil {
					rejectUnknown("$.defaults.retry.when", w, "statusIn", "networkErrors", "timeouts")
				}
			}
		}
		if hooks := asMap(doc.Raw["hooks"]); hooks != nil {
			rejectUnknown("$.hooks", hooks, "beforeAll", "afterAll", "beforeEach", "afterEach")
			for _, hookName := range []string{"beforeAll", "afterAll", "beforeEach", "afterEach"} {
				for si, sv := range asSlice(hooks[hookName]) {
					validateNestedStepShape(&d, doc, fmt.Sprintf("$.hooks.%s[%d]", hookName, si), asMap(sv), rejectUnknown)
				}
			}
		}
		for ti, tv := range asSlice(doc.Raw["tests"]) {
			tm := asMap(tv)
			path := fmt.Sprintf("$.tests[%d]", ti)
			rejectUnknown(path, tm, "id", "name", "tags", "skip", "skipReason", "timeoutMs", "variables", "cases", "steps")
			for caseID, cv := range asMap(tm["cases"]) {
				cp := path + ".cases." + caseID
				cm := asMap(cv)
				if cm == nil {
					d = append(d, diag(doc, cp, "invalid-test-case", "test case must be a mapping"))
					continue
				}
				rejectUnknown(cp, cm, "name", "variables", "skip")
			}
			for si, sv := range asSlice(tm["steps"]) {
				validateNestedStepShape(&d, doc, fmt.Sprintf("%s.steps[%d]", path, si), asMap(sv), rejectUnknown)
			}
		}
	}
	return d
}

func validateNestedStepShape(d *[]Diagnostic, doc LoadedDocument, sp string, sm map[string]any, rejectUnknown func(string, map[string]any, ...string)) {
	if sm == nil {
		return
	}
	rejectUnknown(sp, sm, "id", "name", "skip", "continueOnFailure", "variables", "wait", "use", "with", "request", "extend", "overrides", "timeoutMs", "expect", "extract", "retry", "repeat", "log", "artifacts")
	if rm := asMap(sm["request"]); rm != nil {
		rejectUnknown(sp+".request", rm, requestFieldNames()...)
		validateRequestNestedShape(d, doc, sp+".request", rm, rejectUnknown)
	}
	if em := asMap(sm["extend"]); em != nil {
		rejectUnknown(sp+".extend", em, requestFieldNames()...)
	}
	if om := asMap(sm["overrides"]); om != nil {
		rejectUnknown(sp+".overrides", om, requestFieldNames()...)
	}
	if wm := asMap(sm["wait"]); wm != nil {
		rejectUnknown(sp+".wait", wm, "beforeMs", "afterMs", "forMs")
	}
	if rm := asMap(sm["retry"]); rm != nil {
		rejectUnknown(sp+".retry", rm, "count", "delayMs", "backoffMultiplier", "maxDelayMs", "when")
		if w := asMap(rm["when"]); w != nil {
			rejectUnknown(sp+".retry.when", w, "statusIn", "networkErrors", "timeouts")
		}
	}
	if rm := asMap(sm["repeat"]); rm != nil {
		rejectUnknown(sp+".repeat", rm, "warmupCount", "count")
	}
	if lm := asMap(sm["log"]); lm != nil {
		rejectUnknown(sp+".log", lm, "request", "response")
		if side := asMap(lm["request"]); side != nil {
			rejectUnknown(sp+".log.request", side, "headers", "body")
		}
		if side := asMap(lm["response"]); side != nil {
			rejectUnknown(sp+".log.response", side, "headers", "body")
		}
	}
	if am := asMap(sm["artifacts"]); am != nil {
		rejectUnknown(sp+".artifacts", am, "saveResponseBodyTo", "saveParsedJsonTo", "saveHeadersTo", "saveTimingTo")
	}
	if ex := asMap(sm["expect"]); ex != nil {
		validateExpectationShape(d, doc, sp+".expect", ex, rejectUnknown)
	}
	for name, ev := range asMap(sm["extract"]) {
		rejectUnknown(sp+".extract."+name, asMap(ev), "from", "fromDefinition", "fromHeader", "fromCookie", "fromTextRegex", "fromStatus", "scope", "required", "sensitive")
	}
}

func requestFieldNames() []string {
	return []string{"method", "url", "baseUrl", "path", "pathParams", "pathParamEncoding", "query", "headers", "cookies", "auth", "body", "bodyRaw", "bodyFile", "bodyFileMode", "form", "multipart", "timeoutMs", "followRedirects"}
}

func (e *Engine) ValidateProject(projectRoot string) (ValidationResult, error) {
	disc, err := e.Discover(projectRoot)
	if err != nil {
		return ValidationResult{}, err
	}
	_, configDiagnostics := LoadProjectConfig(disc.ProjectRoot)
	all := append([]Diagnostic{}, configDiagnostics...)
	seenSuiteIDs, seenRequestIDs, seenEnvNames := map[string]string{}, map[string]string{}, map[string]string{}
	for _, suitePath := range disc.Suites {
		doc, err := loadDocument(suitePath)
		if err != nil {
			all = append(all, loadDiagnostic(suitePath, err))
			continue
		}
		if s, ok := doc.Typed.(TestSuiteSpec); ok && s.Metadata.ID != "" {
			if prev, exists := seenSuiteIDs[s.Metadata.ID]; exists {
				all = append(all, NewDiagnostic("duplicate-suite-id", fmt.Sprintf("Duplicate suite id %q; also used by %s", s.Metadata.ID, prev), ErrorSeverity, DocumentLocation{File: suitePath, DocumentPath: "$.id"}))
			} else {
				seenSuiteIDs[s.Metadata.ID] = suitePath
			}
		}
		r, err := e.Validate(suitePath, "", disc.ProjectRoot)
		if err != nil {
			all = append(all, loadDiagnostic(suitePath, err))
			continue
		}
		all = append(all, r.Diagnostics...)
	}
	for _, requestPath := range disc.Requests {
		doc, err := loadDocument(requestPath)
		if err != nil {
			all = append(all, loadDiagnostic(requestPath, err))
			continue
		}
		all = append(all, validateDocument(doc)...)
		if r, ok := doc.Typed.(RequestDefinitionSpec); ok {
			all = append(all, validateBodyFileForDocument(disc.ProjectRoot, doc, "$.request", r.Request)...)
		}
		if r, ok := doc.Typed.(RequestDefinitionSpec); ok && r.Metadata.ID != "" {
			if prev, exists := seenRequestIDs[r.Metadata.ID]; exists {
				all = append(all, NewDiagnostic("duplicate-request-id", fmt.Sprintf("Duplicate request id %q; also used by %s", r.Metadata.ID, prev), ErrorSeverity, DocumentLocation{File: requestPath, DocumentPath: "$.id"}))
			} else {
				seenRequestIDs[r.Metadata.ID] = requestPath
			}
		}
	}
	for _, envPath := range disc.Environments {
		doc, err := loadDocument(envPath)
		if err != nil {
			all = append(all, loadDiagnostic(envPath, err))
			continue
		}
		all = append(all, validateDocument(doc)...)
		if _, inheritanceErr := loadEnvironmentDocument(disc.ProjectRoot, envPath); inheritanceErr != nil {
			all = append(all, NewDiagnostic("invalid-environment-inheritance", inheritanceErr.Error(), ErrorSeverity, DocumentLocation{File: envPath, DocumentPath: "$.extends"}))
		}
		if env, ok := doc.Typed.(EnvironmentSpec); ok {
			name := strings.ToLower(strings.TrimSpace(env.Name))
			if name == "" {
				base := filepath.Base(envPath)
				name = strings.ToLower(strings.TrimSuffix(base, filepath.Ext(base)))
			}
			if prev, exists := seenEnvNames[name]; exists {
				all = append(all, NewDiagnostic("duplicate-environment-name", fmt.Sprintf("Duplicate environment name %q; also used by %s", name, prev), ErrorSeverity, DocumentLocation{File: envPath, DocumentPath: "$.name"}))
			} else {
				seenEnvNames[name] = envPath
			}
		}
	}
	all = dedupeDiagnostics(all)
	sortDiagnostics(all)
	return ValidationResult{Diagnostics: all}, nil
}

func loadDiagnostic(path string, err error) Diagnostic {
	return loadErrorDiagnostic(err, path)
}

func dedupeDiagnostics(ds []Diagnostic) []Diagnostic {
	seen := map[string]bool{}
	out := []Diagnostic{}
	for _, d := range ds {
		key := fmt.Sprintf("%s|%s|%s|%s", d.Location.File, d.Location.DocumentPath, d.Code, d.Message)
		if !seen[key] {
			seen[key] = true
			out = append(out, d)
		}
	}
	return out
}

func sortDiagnostics(ds []Diagnostic) {
	sort.SliceStable(ds, func(i, j int) bool {
		a, b := ds[i], ds[j]
		if a.Location.File != b.Location.File {
			return a.Location.File < b.Location.File
		}
		if a.Location.Line != b.Location.Line {
			return a.Location.Line < b.Location.Line
		}
		if a.Location.Column != b.Location.Column {
			return a.Location.Column < b.Location.Column
		}
		if a.Location.DocumentPath != b.Location.DocumentPath {
			return a.Location.DocumentPath < b.Location.DocumentPath
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.Message < b.Message
	})
}

func validateHTTPURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("Invalid HTTP URL %q: %v", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("URL must use http or https: %s", raw)
	}
	if u.Host == "" {
		return fmt.Errorf("URL must include a host: %s", raw)
	}
	return nil
}

func containsPlaceholder(v any) bool {
	switch x := v.(type) {
	case string:
		return strings.Contains(x, "{{")
	case map[string]any:
		for _, vv := range x {
			if containsPlaceholder(vv) {
				return true
			}
		}
	case []any:
		for _, vv := range x {
			if containsPlaceholder(vv) {
				return true
			}
		}
	}
	return false
}

func validateBundleFiles(bundle LoadedBundle) []Diagnostic {
	d := []Diagnostic{}
	if suite, ok := bundle.Suite.Typed.(TestSuiteSpec); ok {
		for _, ref := range suiteStepRefs(suite) {
			if ref.Step.Request != nil {
				d = append(d, validateBodyFileForDocument(bundle.ProjectRoot, bundle.Suite, ref.Path+".request", *ref.Step.Request)...)
			}
		}
	}
	requestPaths := make([]string, 0, len(bundle.ReferencedRequests))
	for p := range bundle.ReferencedRequests {
		requestPaths = append(requestPaths, p)
	}
	sort.Strings(requestPaths)
	for _, p := range requestPaths {
		doc := bundle.ReferencedRequests[p]
		if rd, ok := doc.Typed.(RequestDefinitionSpec); ok {
			d = append(d, validateBodyFileForDocument(bundle.ProjectRoot, doc, "$.request", rd.Request)...)
		}
	}
	return d
}

func validateBodyFileForDocument(projectRoot string, doc LoadedDocument, path string, r RequestSpec) []Diagnostic {
	if r.BodyFile == "" || strings.Contains(r.BodyFile, "{{") {
		return nil
	}
	p := r.BodyFile
	if !filepath.IsAbs(p) {
		p = filepath.Join(projectRoot, p)
	}
	if _, err := ensureExistingPathWithinRoot(projectRoot, p, "bodyFile"); err != nil {
		return []Diagnostic{diag(doc, path+".bodyFile", "invalid-body-file", fmt.Sprintf("bodyFile %q is not safely readable: %v", r.BodyFile, err))}
	}
	return nil
}

func validateRequestNestedShape(d *[]Diagnostic, doc LoadedDocument, path string, raw map[string]any, rejectUnknown func(string, map[string]any, ...string)) {
	if pm := asMap(raw["pathParamEncoding"]); pm != nil {
		rejectUnknown(path+".pathParamEncoding", pm, "enabled")
	}
	if am := asMap(raw["auth"]); am != nil {
		rejectUnknown(path+".auth", am, "type", "username", "password", "token", "name", "value", "in")
	}
	for name, v := range asMap(raw["multipart"]) {
		if m := asMap(v); m != nil {
			rejectUnknown(path+".multipart."+name, m, "file", "filename", "contentType")
		}
	}
}

var assertionOperatorNames = setOf(
	"exists", "isNull", "type", "equals", "notEquals", "equalsIgnoreCase",
	"in", "notIn", "matches", "contains", "notContains", "startsWith", "endsWith",
	"count", "minCount", "maxCount", "length", "minLength", "maxLength", "unique",
	"greaterThan", "greaterThanOrEqual", "lessThan", "lessThanOrEqual",
	"before", "after", "onOrBefore", "onOrAfter", "sha256",
)

func validateExpectationShape(d *[]Diagnostic, doc LoadedDocument, path string, raw map[string]any, rejectUnknown func(string, map[string]any, ...string)) {
	rejectUnknown(path, raw, "status", "body", "headers", "text", "binary", "performance")
	if status, exists := raw["status"]; exists && !containsPlaceholder(status) {
		valid := false
		switch v := status.(type) {
		case int:
			valid = v >= 100 && v <= 599
		case int64:
			valid = v >= 100 && v <= 599
		case []any:
			valid = len(v) > 0
			for _, item := range v {
				code := asInt(item)
				if !isIntegerValue(item) || code < 100 || code > 599 {
					valid = false
					break
				}
			}
		}
		if !valid {
			*d = append(*d, diag(doc, path+".status", "invalid-status-expectation", "expect.status must be an HTTP status code, a non-empty list of status codes, or a variable placeholder"))
		}
	}
	if body := asMap(raw["body"]); body != nil {
		validateAssertionShape(d, doc, path+".body", body, rejectUnknown)
	}
	for name, v := range asMap(raw["headers"]) {
		validateOperatorMap(d, doc, path+".headers."+name, asMap(v))
	}
	if m := asMap(raw["text"]); m != nil {
		validateOperatorMap(d, doc, path+".text", m)
	}
	if m := asMap(raw["binary"]); m != nil {
		validateOperatorMap(d, doc, path+".binary", m)
	}
	for metric, v := range asMap(raw["performance"]) {
		if metric != "totalMs" && metric != "avgMs" && metric != "p95Ms" && metric != "maxMs" {
			*d = append(*d, diag(doc, path+".performance."+metric, "unknown-performance-metric", fmt.Sprintf("Unknown performance metric %q", metric)))
		}
		validateOperatorMap(d, doc, path+".performance."+metric, asMap(v))
	}
}

func validateAssertionShape(d *[]Diagnostic, doc LoadedDocument, path string, raw map[string]any, rejectUnknown func(string, map[string]any, ...string)) {
	allowed := []string{"path", "field", "and", "or"}
	for op := range assertionOperatorNames {
		allowed = append(allowed, op)
	}
	rejectUnknown(path, raw, allowed...)
	operatorCount := 0
	for key := range raw {
		if key != "path" && key != "field" && key != "and" && key != "or" {
			operatorCount++
		}
	}
	if asString(raw["path"]) == "" && (operatorCount > 0 || asString(raw["field"]) != "") {
		*d = append(*d, diag(doc, path, "assertion-path-required", "Assertion operators and field require a path; pathless clauses may only group 'and'/'or' branches"))
	}
	validateOperatorMap(d, doc, path, raw)
	for i, v := range asSlice(raw["and"]) {
		if m := asMap(v); m != nil {
			validateAssertionShape(d, doc, fmt.Sprintf("%s.and[%d]", path, i), m, rejectUnknown)
		}
	}
	for i, v := range asSlice(raw["or"]) {
		if m := asMap(v); m != nil {
			validateAssertionShape(d, doc, fmt.Sprintf("%s.or[%d]", path, i), m, rejectUnknown)
		}
	}
}

func validateOperatorMap(d *[]Diagnostic, doc LoadedDocument, path string, raw map[string]any) {
	keys := make([]string, 0, len(raw))
	for op := range raw {
		if op == "path" || op == "field" || op == "and" || op == "or" {
			continue
		}
		keys = append(keys, op)
	}
	sort.Strings(keys)
	for _, op := range keys {
		if !assertionOperatorNames[op] {
			*d = append(*d, diag(doc, path+"."+op, "unknown-assertion-operator", fmt.Sprintf("Unknown assertion operator %q", op)))
			continue
		}
		value := raw[op]
		if op == "matches" {
			if pattern, ok := value.(string); ok && !strings.Contains(pattern, "{{") {
				if _, err := regexp.Compile(pattern); err != nil {
					*d = append(*d, diag(doc, path+"."+op, "invalid-regex", fmt.Sprintf("Invalid regular expression: %v", err)))
				}
			}
		}
		if containsPlaceholder(value) {
			continue
		}
		valid := true
		switch op {
		case "exists", "isNull":
			_, valid = value.(bool)
		case "unique":
			_, valid = value.(bool)
		case "type":
			t := strings.ToLower(asString(value))
			valid = t == "null" || t == "boolean" || t == "number" || t == "string" || t == "array" || t == "object"
		case "in", "notIn":
			_, valid = value.([]any)
		case "count", "minCount", "maxCount", "length", "minLength", "maxLength":
			valid = isIntegerValue(value) && asInt(value) >= 0
		case "greaterThan", "greaterThanOrEqual", "lessThan", "lessThanOrEqual":
			_, valid = asFloat(value)
		case "matches", "before", "after", "onOrBefore", "onOrAfter", "startsWith", "endsWith", "equalsIgnoreCase", "sha256":
			_, valid = value.(string)
		}
		if !valid {
			*d = append(*d, diag(doc, path+"."+op, "invalid-assertion-operand", fmt.Sprintf("Invalid operand for assertion operator %q", op)))
		}
	}
}

func SelectEnvironment(projectRoot, selector string) (string, error) {
	if selector == "" {
		return "", nil
	}
	if filepath.IsAbs(selector) || filepath.Dir(selector) != "." || filepath.Ext(selector) != "" {
		return "", fmt.Errorf("environment must be selected by name (for example -e dev), not by path: %s", selector)
	}
	disc, err := discoverProject(projectRoot)
	if err != nil {
		return "", err
	}
	matches := []string{}
	for _, p := range disc.Environments {
		base := filepath.Base(p)
		name := base[:len(base)-len(filepath.Ext(base))]
		if equalFold(name, selector) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 0 {
		rootAbs, absErr := absPath(projectRoot)
		if absErr != nil {
			return "", fmt.Errorf("could not resolve project root while selecting environment %q: %w", selector, absErr)
		}
		return "", fmt.Errorf("could not find environment %q under %s", selector, filepath.Join(rootAbs, "environments"))
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("environment selector %q matched multiple files", selector)
	}
	return matches[0], nil
}
func equalFold(a, b string) bool { return len(a) == len(b) && stringsEqualFold(a, b) }
func stringsEqualFold(a, b string) bool { // avoid another import solely for one call in generated file
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 32
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 32
		}
		if ca != cb {
			return false
		}
	}
	return true
}
