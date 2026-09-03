package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

const DefaultMaxSpecBytes int64 = 10 << 20 // 10 MiB

func loadDocument(path string) (LoadedDocument, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return LoadedDocument{}, userLoadError("load-error", path, "cannot resolve path: %v", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return LoadedDocument{}, userLoadError("load-error", abs, "cannot read spec: %v", err)
	}
	if info.Size() > DefaultMaxSpecBytes {
		return LoadedDocument{}, userLoadError("spec-too-large", abs, "spec file exceeds maximum size of %d bytes", DefaultMaxSpecBytes)
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return LoadedDocument{}, userLoadError("load-error", abs, "cannot read spec: %v", err)
	}
	raw, smap, err := parseYAML(data)
	if err != nil {
		return LoadedDocument{}, classifyYAMLLoadError(abs, err)
	}
	typed, err := parseTypedDocumentWithSource(abs, raw, smap)
	if err != nil {
		return LoadedDocument{}, err
	}
	return LoadedDocument{Path: filepath.Clean(abs), Raw: raw, Typed: typed, SourceMap: smap}, nil
}

func parseTypedDocument(path string, raw map[string]any) (any, error) {
	return parseTypedDocumentWithSource(path, raw, nil)
}

func parseTypedDocumentWithSource(path string, raw map[string]any, smap map[string][2]int) (any, error) {
	at := func(docPath string) (int, int) {
		if p, ok := smap[docPath]; ok {
			return p[0], p[1]
		}
		return 0, 0
	}
	fmtRaw, hasFormat := raw["formatVersion"]
	if !hasFormat || fmtRaw == nil || strings.TrimSpace(asString(fmtRaw)) == "" {
		line, col := at("$.formatVersion")
		return nil, &LoadError{Name: "missing-format-version", Path: path, Line: line, Column: col, Message: "missing formatVersion"}
	}
	fmtv, ok := fmtRaw.(string)
	if !ok {
		line, col := at("$.formatVersion")
		return nil, &LoadError{Name: "invalid-type", Path: path, Line: line, Column: col, Message: "formatVersion must be a string"}
	}
	if fmtv != SpecFormatVersion {
		line, col := at("$.formatVersion")
		return nil, &LoadError{Name: "unsupported-format-version", Path: path, Line: line, Column: col, Message: fmt.Sprintf("unsupported formatVersion %q; this Veriflow build supports exactly %q", fmtv, SpecFormatVersion)}
	}
	kindRaw, hasKind := raw["kind"]
	if !hasKind || kindRaw == nil || strings.TrimSpace(asString(kindRaw)) == "" {
		line, col := at("$.kind")
		return nil, &LoadError{Name: "missing-kind", Path: path, Line: line, Column: col, Message: "missing kind"}
	}
	kindString, ok := kindRaw.(string)
	if !ok {
		line, col := at("$.kind")
		return nil, &LoadError{Name: "invalid-type", Path: path, Line: line, Column: col, Message: "kind must be a string"}
	}
	kind := SpecKind(kindString)
	meta := SpecMetadata{FormatVersion: fmtv, Kind: kind, ID: asString(raw["id"]), Name: asString(raw["name"]), Description: asString(raw["description"]), Source: &SourceRef{File: path, DocumentPath: "$", Line: 1, Column: 1}}
	if info := asMap(raw["info"]); info != nil {
		if meta.Name == "" {
			meta.Name = asString(info["name"])
		}
		if meta.Description == "" {
			meta.Description = asString(info["description"])
		}
	}
	var typed any
	var err error
	switch kind {
	case RequestDefinitionKind:
		typed, err = parseRequestDefinition(path, raw, meta)
	case TestSuiteKind:
		typed, err = parseSuite(raw, meta)
	case EnvironmentKind:
		typed = EnvironmentSpec{Metadata: meta, Name: asString(raw["name"]), Extends: asString(raw["extends"]), Variables: cloneMap(asMap(raw["variables"])), Raw: raw}
	default:
		line, col := at("$.kind")
		return nil, &LoadError{Name: "unsupported-kind", Path: path, Line: line, Column: col, Message: fmt.Sprintf("unsupported kind %q", kind)}
	}
	if err != nil {
		return nil, &LoadError{Name: "load-error", Path: path, Message: err.Error(), Cause: err}
	}
	return typed, nil
}

func parseRequestDefinition(path string, raw map[string]any, meta SpecMetadata) (RequestDefinitionSpec, error) {
	reqRaw := asMap(raw["request"])
	if reqRaw == nil {
		reqRaw = map[string]any{}
	}
	req, err := parseRequest(reqRaw)
	if err != nil {
		return RequestDefinitionSpec{}, err
	}
	inputs := map[string]InputDefinition{}
	for name, vv := range asMap(raw["inputs"]) {
		v := asMap(vv)
		inputs[name] = InputDefinition{Type: asString(v["type"]), Required: asBool(v["required"]), Sensitive: asBool(v["sensitive"]), Description: asString(v["description"]), Default: deepCopy(v["default"])}
	}
	outputs := map[string]OutputDefinition{}
	for name, vv := range asMap(raw["outputs"]) {
		v := asMap(vv)
		outputs[name] = OutputDefinition{Path: asString(v["path"]), Required: asBool(v["required"]), Sensitive: asBool(v["sensitive"])}
	}
	return RequestDefinitionSpec{Metadata: meta, Request: req, Inputs: inputs, Outputs: outputs, Raw: raw, Path: path}, nil
}

func parseSuite(raw map[string]any, meta SpecMetadata) (TestSuiteSpec, error) {
	infoRaw := asMap(raw["info"])
	suite := TestSuiteSpec{Metadata: meta, Info: SuiteInfo{Name: asString(infoRaw["name"]), Description: asString(infoRaw["description"])}, TimeoutMS: asInt(raw["timeoutMs"]), Globals: GlobalsSpec{Variables: map[string]any{}}, Defaults: SuiteDefaults{Headers: map[string]any{}}, Raw: raw}
	if g := asMap(raw["globals"]); g != nil {
		suite.Globals.Variables = cloneMap(asMap(g["variables"]))
	}
	if d := asMap(raw["defaults"]); d != nil {
		suite.Defaults.TimeoutMS = asInt(d["timeoutMs"])
		suite.Defaults.Headers = cloneMap(asMap(d["headers"]))
		if _, ok := d["followRedirects"]; ok {
			b := asBool(d["followRedirects"])
			suite.Defaults.FollowRedirects = &b
		}
		if r := asMap(d["retry"]); r != nil {
			retry := parseRetrySpec(r)
			suite.Defaults.Retry = &retry
		}
	}
	if hooks := asMap(raw["hooks"]); hooks != nil {
		var err error
		if suite.Hooks.BeforeAll, err = parseStepList(asSlice(hooks["beforeAll"])); err != nil {
			return TestSuiteSpec{}, fmt.Errorf("hooks.beforeAll: %w", err)
		}
		if suite.Hooks.AfterAll, err = parseStepList(asSlice(hooks["afterAll"])); err != nil {
			return TestSuiteSpec{}, fmt.Errorf("hooks.afterAll: %w", err)
		}
		if suite.Hooks.BeforeEach, err = parseStepList(asSlice(hooks["beforeEach"])); err != nil {
			return TestSuiteSpec{}, fmt.Errorf("hooks.beforeEach: %w", err)
		}
		if suite.Hooks.AfterEach, err = parseStepList(asSlice(hooks["afterEach"])); err != nil {
			return TestSuiteSpec{}, fmt.Errorf("hooks.afterEach: %w", err)
		}
	}
	for _, tv := range asSlice(raw["tests"]) {
		tm := asMap(tv)
		if tm == nil {
			continue
		}
		t := TestSpec{ID: asString(tm["id"]), Name: asString(tm["name"]), Tags: asStringSlice(tm["tags"]), Skip: asBool(tm["skip"]), SkipReason: asString(tm["skipReason"]), TimeoutMS: asInt(tm["timeoutMs"]), Variables: cloneMap(asMap(tm["variables"])), Cases: map[string]TestCaseSpec{}}
		for caseID, cv := range asMap(tm["cases"]) {
			cm := asMap(cv)
			if cm == nil {
				continue
			}
			t.Cases[caseID] = TestCaseSpec{Name: asString(cm["name"]), Variables: cloneMap(asMap(cm["variables"])), Skip: asBool(cm["skip"])}
		}
		for _, sv := range asSlice(tm["steps"]) {
			sm := asMap(sv)
			if sm == nil {
				continue
			}
			step, err := parseStep(sm)
			if err != nil {
				return TestSuiteSpec{}, err
			}
			t.Steps = append(t.Steps, step)
		}
		suite.Tests = append(suite.Tests, t)
	}
	return suite, nil
}

func parseStepList(raw []any) ([]StepSpec, error) {
	steps := make([]StepSpec, 0, len(raw))
	for _, value := range raw {
		m := asMap(value)
		if m == nil {
			// Keep parsing permissive here; raw semantic validation reports the
			// precise location/type error instead of turning it into a load failure.
			continue
		}
		step, err := parseStep(m)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func parseStep(sm map[string]any) (StepSpec, error) {
	step := StepSpec{ID: asString(sm["id"]), Name: asString(sm["name"]), Skip: asBool(sm["skip"]), ContinueOnFailure: asBool(sm["continueOnFailure"]), Variables: cloneMap(asMap(sm["variables"])), Use: asString(sm["use"]), With: cloneMap(asMap(sm["with"])), Extend: cloneMap(asMap(sm["extend"])), Overrides: cloneMap(asMap(sm["overrides"])), TimeoutMS: asInt(sm["timeoutMs"]), Extract: map[string]ExtractionSpec{}}
	if rm := asMap(sm["request"]); rm != nil {
		r, err := parseRequest(rm)
		if err != nil {
			return step, err
		}
		step.Request = &r
	}
	if w := asMap(sm["wait"]); w != nil {
		step.Wait = &WaitSpec{BeforeMS: asInt(w["beforeMs"]), AfterMS: asInt(w["afterMs"]), ForMS: asInt(w["forMs"])}
	}
	if r := asMap(sm["retry"]); r != nil {
		retry := parseRetrySpec(r)
		step.Retry = &retry
	}
	if r := asMap(sm["repeat"]); r != nil {
		count := asInt(r["count"])
		if count == 0 {
			count = 1
		}
		step.Repeat = &RepeatSpec{WarmupCount: asInt(r["warmupCount"]), Count: count}
	}
	if a := asMap(sm["artifacts"]); a != nil {
		step.Artifacts = &ArtifactSpec{SaveResponseBodyTo: asString(a["saveResponseBodyTo"]), SaveParsedJSONTo: asString(a["saveParsedJsonTo"]), SaveHeadersTo: asString(a["saveHeadersTo"]), SaveTimingTo: asString(a["saveTimingTo"])}
	}
	if l := asMap(sm["log"]); l != nil {
		step.Log = &LogSpec{}
		if req := asMap(l["request"]); req != nil {
			if _, ok := req["headers"]; ok {
				b := asBool(req["headers"])
				step.Log.Request.Headers = &b
			}
			if _, ok := req["body"]; ok {
				b := asBool(req["body"])
				step.Log.Request.Body = &b
			}
		}
		if resp := asMap(l["response"]); resp != nil {
			if _, ok := resp["headers"]; ok {
				b := asBool(resp["headers"])
				step.Log.Response.Headers = &b
			}
			if _, ok := resp["body"]; ok {
				b := asBool(resp["body"])
				step.Log.Response.Body = &b
			}
		}
	}
	if e := asMap(sm["expect"]); e != nil {
		step.Expect = parseExpectation(e)
	}
	for name, ev := range asMap(sm["extract"]) {
		em := asMap(ev)
		scope := VariableScope(asString(em["scope"]))
		if scope == "" {
			scope = TestScope
		}
		step.Extract[name] = ExtractionSpec{FromSelector: asString(em["from"]), FromDefinition: asString(em["fromDefinition"]), FromHeader: asString(em["fromHeader"]), FromCookie: asString(em["fromCookie"]), FromTextRegex: asString(em["fromTextRegex"]), FromStatus: asBool(em["fromStatus"]), Scope: scope, Required: asBool(em["required"]), Sensitive: asBool(em["sensitive"])}
	}
	return step, nil
}

func parseRetrySpec(r map[string]any) RetrySpec {
	when := asMap(r["when"])
	multiplier := 1.0
	if v, ok := asFloat(r["backoffMultiplier"]); ok && v > 0 {
		multiplier = v
	}
	return RetrySpec{
		Count: asInt(r["count"]), DelayMS: asInt(r["delayMs"]),
		BackoffMultiplier: multiplier, MaxDelayMS: asInt(r["maxDelayMs"]),
		When: RetryCondition{StatusIn: asIntSlice(when["statusIn"]), NetworkErrors: asBool(when["networkErrors"]), Timeouts: asBool(when["timeouts"])},
	}
}

func parseExpectation(raw map[string]any) *ExpectationSpec {
	e := &ExpectationSpec{Status: raw["status"], Headers: map[string]HeaderExpectation{}}
	for name, v := range asMap(raw["headers"]) {
		e.Headers[name] = HeaderExpectation{Operators: cloneMap(asMap(v))}
	}
	if body := asMap(raw["body"]); body != nil {
		c := parseAssertionClause(body)
		e.Body = &c
	}
	if txt := asMap(raw["text"]); txt != nil {
		e.Text = &TextExpectation{Operators: cloneMap(txt)}
	}
	if bin := asMap(raw["binary"]); bin != nil {
		e.Binary = &BinaryExpectation{Operators: cloneMap(bin)}
	}
	if perf := asMap(raw["performance"]); perf != nil {
		metrics := map[string]map[string]any{}
		for k, v := range perf {
			metrics[k] = cloneMap(asMap(v))
		}
		e.Performance = &PerformanceExpectation{Metrics: metrics}
	}
	return e
}

func parseAssertionClause(raw map[string]any) AssertionClause {
	c := AssertionClause{Path: asString(raw["path"]), ElementField: asString(raw["field"]), Operators: map[string]any{}}
	for k, v := range raw {
		switch k {
		case "path", "field", "and", "or":
		default:
			c.Operators[k] = deepCopy(v)
		}
	}
	for _, v := range asSlice(raw["and"]) {
		if m := asMap(v); m != nil {
			c.And = append(c.And, parseAssertionClause(m))
		}
	}
	for _, v := range asSlice(raw["or"]) {
		if m := asMap(v); m != nil {
			c.Or = append(c.Or, parseAssertionClause(m))
		}
	}
	return c
}

func parseRequest(raw map[string]any) (RequestSpec, error) {
	method := asString(raw["method"])
	r := RequestSpec{Method: method, URL: asString(raw["url"]), BaseURL: asString(raw["baseUrl"]), Path: asString(raw["path"]), PathParams: cloneMap(asMap(raw["pathParams"])), Query: cloneMap(asMap(raw["query"])), Headers: cloneMap(asMap(raw["headers"])), Cookies: cloneMap(asMap(raw["cookies"])), Body: deepCopy(raw["body"]), BodyRaw: asString(raw["bodyRaw"]), BodyFile: asString(raw["bodyFile"]), BodyFileMode: asString(raw["bodyFileMode"]), Form: cloneMap(asMap(raw["form"])), Multipart: cloneMap(asMap(raw["multipart"])), TimeoutMS: asInt(raw["timeoutMs"])}
	if a := asMap(raw["auth"]); a != nil {
		r.Auth = &AuthSpec{Type: asString(a["type"]), Username: asString(a["username"]), Password: asString(a["password"]), Token: asString(a["token"]), Name: asString(a["name"]), Value: asString(a["value"]), In: asString(a["in"])}
	}
	if p := asMap(raw["pathParamEncoding"]); p != nil {
		r.PathParamEncoding.Enabled = asBool(p["enabled"])
	}
	if _, ok := raw["followRedirects"]; ok {
		b := asBool(raw["followRedirects"])
		r.FollowRedirects = &b
	}
	return r, nil
}

func asMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return nil
}
func asSlice(v any) []any {
	if s, ok := v.([]any); ok {
		return s
	}
	return nil
}
func asString(v any) string {
	if v == nil {
		return ""
	}
	if s, ok := v.(string); ok {
		return s
	}
	return fmt.Sprint(v)
}
func asBool(v any) bool {
	if b, ok := v.(bool); ok {
		return b
	}
	s := strings.ToLower(asString(v))
	return s == "true" || s == "1"
}
func asInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	case string:
		i, _ := strconv.Atoi(x)
		return i
	}
	return 0
}
func asStringSlice(v any) []string {
	out := []string{}
	for _, x := range asSlice(v) {
		out = append(out, asString(x))
	}
	return out
}
func asIntSlice(v any) []int {
	out := []int{}
	for _, x := range asSlice(v) {
		out = append(out, asInt(x))
	}
	return out
}
func cloneMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	out := map[string]any{}
	for k, v := range m {
		out[k] = deepCopy(v)
	}
	return out
}
func deepCopy(v any) any {
	switch x := v.(type) {
	case map[string]any:
		return cloneMap(x)
	case []any:
		y := make([]any, len(x))
		for i := range x {
			y[i] = deepCopy(x[i])
		}
		return y
	default:
		return x
	}
}

func loadBundle(suitePath string, environmentPath string, projectRoot string) (LoadedBundle, error) {
	suite, err := loadDocument(suitePath)
	if err != nil {
		return LoadedBundle{}, err
	}
	if projectRoot == "" {
		projectRoot, err = inferProjectRoot(suite.Path)
		if err != nil {
			return LoadedBundle{}, userLoadError("load-error", suite.Path, "cannot infer project root: %v", err)
		}
	}
	projectRoot, err = absPath(projectRoot)
	if err != nil {
		return LoadedBundle{}, userLoadError("load-error", projectRoot, "cannot resolve project root: %v", err)
	}
	bundle := LoadedBundle{Suite: suite, ReferencedRequests: map[string]LoadedDocument{}, ProjectRoot: projectRoot}
	if environmentPath != "" {
		env, err := loadEnvironmentDocument(projectRoot, environmentPath)
		if err != nil {
			return LoadedBundle{}, err
		}
		bundle.Environment = &env
	}
	typed, ok := suite.Typed.(TestSuiteSpec)
	if !ok {
		return LoadedBundle{}, userLoadError("invalid-suite-kind", suite.Path, "suite file must have kind testSuite")
	}
	loadReferencedStep := func(step StepSpec) {
		if step.Use == "" {
			return
		}
		resolved, err := ResolveRequestReference(projectRoot, step.Use)
		if err != nil {
			// Cross-file semantic validation reports the reference itself at the
			// suite location. There is no referenced document to diagnose here.
			return
		}
		if _, exists := bundle.ReferencedRequests[resolved]; exists {
			return
		}
		doc, err := loadDocument(resolved)
		if err != nil {
			if diagnostic, ok := DiagnosticFromError(err, resolved); ok {
				bundle.ReferenceDiagnostics = append(bundle.ReferenceDiagnostics, diagnostic)
			} else {
				bundle.ReferenceDiagnostics = append(bundle.ReferenceDiagnostics, NewDiagnostic("load-error", err.Error(), ErrorSeverity, DocumentLocation{File: resolved, DocumentPath: "$"}))
			}
			return
		}
		bundle.ReferencedRequests[resolved] = doc
	}
	for _, steps := range [][]StepSpec{typed.Hooks.BeforeAll, typed.Hooks.AfterAll, typed.Hooks.BeforeEach, typed.Hooks.AfterEach} {
		for _, step := range steps {
			loadReferencedStep(step)
		}
	}
	for _, test := range typed.Tests {
		for _, step := range test.Steps {
			loadReferencedStep(step)
		}
	}
	return bundle, nil
}

// loadEnvironmentDocument resolves environment inheritance by name. Parent
// variables are deep-merged first, then the child overrides them. The selected
// document remains attributed to the child file for diagnostics and reporting.
func loadEnvironmentDocument(projectRoot, path string) (LoadedDocument, error) {
	var err error
	projectRoot, err = absPath(projectRoot)
	if err != nil {
		return LoadedDocument{}, userLoadError("invalid-environment-inheritance", projectRoot, "cannot resolve project root: %v", err)
	}
	seen := map[string]int{}
	chain := []string{}
	var load func(string) (LoadedDocument, error)
	load = func(current string) (LoadedDocument, error) {
		abs, err := absPath(current)
		if err != nil {
			return LoadedDocument{}, userLoadError("invalid-environment-inheritance", current, "cannot resolve environment path: %v", err)
		}
		if idx, ok := seen[abs]; ok {
			cycle := append(append([]string{}, chain[idx:]...), filepath.Base(abs))
			for i := range cycle {
				cycle[i] = strings.TrimSuffix(cycle[i], filepath.Ext(cycle[i]))
			}
			return LoadedDocument{}, userLoadError("invalid-environment-inheritance", abs, "environment inheritance cycle detected: %s", strings.Join(cycle, " -> "))
		}
		seen[abs] = len(chain)
		chain = append(chain, filepath.Base(abs))
		defer func() {
			delete(seen, abs)
			chain = chain[:len(chain)-1]
		}()

		doc, err := loadDocument(abs)
		if err != nil {
			return LoadedDocument{}, err
		}
		env, ok := doc.Typed.(EnvironmentSpec)
		if !ok {
			return LoadedDocument{}, userLoadError("invalid-environment-inheritance", abs, "selected environment must have kind environment")
		}
		if strings.TrimSpace(env.Extends) == "" {
			return doc, nil
		}
		parentPath, err := SelectEnvironment(projectRoot, env.Extends)
		if err != nil {
			return LoadedDocument{}, &LoadError{Name: "invalid-environment-inheritance", Path: abs, Message: fmt.Sprintf("environment %q extends %q: %v", env.Name, env.Extends, err), Cause: err}
		}
		parentDoc, err := load(parentPath)
		if err != nil {
			return LoadedDocument{}, err
		}
		parent := parentDoc.Typed.(EnvironmentSpec)
		env.Variables = deepMerge(parent.Variables, env.Variables)
		doc.Typed = env
		doc.Raw = cloneMap(doc.Raw)
		doc.Raw["variables"] = cloneMap(env.Variables)
		return doc, nil
	}
	return load(path)
}

// ResolveRequestReference implements issue #2: request references are logical names
// rooted at <project>/requests rather than filesystem paths relative to suites.
func ResolveRequestReference(projectRoot, ref string) (string, error) {
	if strings.TrimSpace(ref) == "" {
		return "", fmt.Errorf("empty request reference")
	}
	if filepath.IsAbs(ref) || strings.HasPrefix(ref, ".") || strings.Contains(filepath.ToSlash(ref), "../") {
		return "", fmt.Errorf("request reference %q must be relative to the project requests directory", ref)
	}
	clean := filepath.Clean(filepath.FromSlash(ref))
	root, err := absPath(filepath.Join(projectRoot, "requests"))
	if err != nil {
		return "", fmt.Errorf("resolve requests directory: %w", err)
	}
	if filepath.Ext(clean) == "" {
		yml, err := absPath(filepath.Join(root, clean+".yml"))
		if err != nil {
			return "", fmt.Errorf("resolve request reference %q: %w", ref, err)
		}
		yaml, err := absPath(filepath.Join(root, clean+".yaml"))
		if err != nil {
			return "", fmt.Errorf("resolve request reference %q: %w", ref, err)
		}
		if _, err := os.Stat(yml); err == nil {
			clean += ".yml"
		} else if _, err := os.Stat(yaml); err == nil {
			clean += ".yaml"
		} else {
			clean += ".yml"
		}
	}
	resolved, err := absPath(filepath.Join(root, clean))
	if err != nil {
		return "", fmt.Errorf("resolve request reference %q: %w", ref, err)
	}
	if !pathWithinRoot(root, resolved) {
		return "", fmt.Errorf("request reference escapes requests directory: %s", ref)
	}
	if _, err := os.Lstat(resolved); err == nil {
		confined, err := ensureExistingPathWithinRoot(root, resolved, "request reference")
		if err != nil {
			return "", err
		}
		resolved = confined
	}
	return resolved, nil
}

func discoverProject(root string) (DiscoveryResult, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return DiscoveryResult{}, userLoadError("project-discovery-error", root, "cannot resolve project root: %v", err)
	}
	root = filepath.Clean(abs)
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return DiscoveryResult{}, userLoadError("project-root-not-found", root, "project root not found")
		}
		return DiscoveryResult{}, userLoadError("project-discovery-error", root, "cannot inspect project root: %v", err)
	}
	if !info.IsDir() {
		return DiscoveryResult{}, userLoadError("project-root-not-found", root, "project root is not a directory")
	}
	result := DiscoveryResult{ProjectRoot: root, FixturesRoot: filepath.Join(root, "fixtures"), ArtifactsRoot: filepath.Join(root, "artifacts")}
	collect := func(sub string) ([]string, error) {
		base := filepath.Join(root, sub)
		if _, err := os.Stat(base); err != nil {
			if os.IsNotExist(err) {
				return nil, nil
			}
			return nil, userLoadError("project-discovery-error", base, "cannot inspect %s directory: %v", sub, err)
		}
		out := []string{}
		err := filepath.WalkDir(base, func(path string, d os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext == ".yml" || ext == ".yaml" {
				out = append(out, path)
			}
			return nil
		})
		if err != nil {
			return nil, userLoadError("project-discovery-error", base, "cannot discover %s: %v", sub, err)
		}
		sort.Strings(out)
		return out, nil
	}
	if result.Suites, err = collect("suites"); err != nil {
		return DiscoveryResult{}, err
	}
	if result.Requests, err = collect("requests"); err != nil {
		return DiscoveryResult{}, err
	}
	if result.Environments, err = collect("environments"); err != nil {
		return DiscoveryResult{}, err
	}
	return result, nil
}

type DiscoveryResult struct {
	ProjectRoot                    string `json:"projectRoot"`
	Suites, Requests, Environments []string
	FixturesRoot, ArtifactsRoot    string
}

func dirExists(path string) bool { st, err := os.Stat(path); return err == nil && st.IsDir() }
