package engine

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"hash/fnv"
	mathrand "math/rand"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	fullPlaceholder     = regexp.MustCompile(`^\{\{\s*([A-Za-z0-9_\.]+)\s*\}\}$`)
	embeddedPlaceholder = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_\.]+)\s*\}\}`)
)

const maxVariableExpansionDepth = 64

type InterpolationError struct {
	Name    string
	Message string
}

func (e InterpolationError) Error() string { return e.Message }

// ResolveVariables fixes issue #4. It resolves aliases inside the merged variable
// map before the request or assertion is rendered, including aliases to extracted
// runtime variables. Cycles and excessive depth return explicit errors.
func ResolveVariables(lookup map[string]any) (map[string]any, error) {
	root := cloneMap(lookup)
	resolved := map[string]any{}
	keys := make([]string, 0, len(root))
	for key := range root {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		v, err := resolveVariablePath(key, root, resolved, nil, 0)
		if err != nil {
			return nil, err
		}
		resolved[key] = v
	}
	return resolved, nil
}

func resolveVariablePath(path string, root, cache map[string]any, stack []string, depth int) (any, error) {
	if depth > maxVariableExpansionDepth {
		return nil, InterpolationError{Name: "variable-expansion-depth", Message: fmt.Sprintf("Variable expansion exceeded maximum depth while resolving '%s'", firstStackName(stack, path))}
	}
	if v, ok := cache[path]; ok {
		return deepCopy(v), nil
	}
	for i, p := range stack {
		if p == path {
			cycle := append(append([]string{}, stack[i:]...), path)
			return nil, InterpolationError{Name: "variable-cycle", Message: fmt.Sprintf("Variable expansion cycle detected: %s", strings.Join(cycle, " -> "))}
		}
	}
	raw, ok := lookupRawPath(root, path)
	if !ok {
		return nil, InterpolationError{Name: "unresolved-variable", Message: fmt.Sprintf("Unresolved variable '%s'", path)}
	}
	v, err := resolveVariableValue(raw, root, cache, append(stack, path), depth+1)
	if err != nil {
		return nil, err
	}
	cache[path] = v
	return deepCopy(v), nil
}

func resolveVariableValue(value any, root, cache map[string]any, stack []string, depth int) (any, error) {
	if depth > maxVariableExpansionDepth {
		return nil, InterpolationError{Name: "variable-expansion-depth", Message: fmt.Sprintf("Variable expansion exceeded maximum depth while resolving '%s'", firstStackName(stack, "value"))}
	}
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, vv := range v {
			resolved, err := resolveVariableValue(vv, root, cache, stack, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = resolved
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i := range v {
			r, err := resolveVariableValue(v[i], root, cache, stack, depth+1)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case string:
		if m := fullPlaceholder.FindStringSubmatch(v); m != nil {
			return resolveVariablePath(m[1], root, cache, stack, depth+1)
		}
		var firstErr error
		out := embeddedPlaceholder.ReplaceAllStringFunc(v, func(token string) string {
			if firstErr != nil {
				return token
			}
			m := embeddedPlaceholder.FindStringSubmatch(token)
			r, err := resolveVariablePath(m[1], root, cache, stack, depth+1)
			if err != nil {
				firstErr = err
				return token
			}
			if r == nil || isComplexValue(r) {
				firstErr = InterpolationError{Name: "unresolved-variable", Message: fmt.Sprintf("Embedded interpolation requires scalar value for '%s'", m[1])}
				return token
			}
			return fmt.Sprint(r)
		})
		if firstErr != nil {
			return nil, firstErr
		}
		return out, nil
	default:
		return deepCopy(v), nil
	}
}

func firstStackName(stack []string, fallback string) string {
	if len(stack) > 0 {
		return stack[0]
	}
	return fallback
}
func isComplexValue(v any) bool {
	switch v.(type) {
	case map[string]any, []any:
		return true
	}
	return false
}

func lookupRawPath(root map[string]any, path string) (any, bool) {
	var cur any = root
	for _, part := range strings.Split(path, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		cur, ok = m[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

func ResolveData(value any, lookup map[string]any) (any, error) {
	switch v := value.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, vv := range v {
			r, err := ResolveData(vv, lookup)
			if err != nil {
				return nil, err
			}
			out[k] = r
		}
		return out, nil
	case []any:
		out := make([]any, len(v))
		for i := range v {
			r, err := ResolveData(v[i], lookup)
			if err != nil {
				return nil, err
			}
			out[i] = r
		}
		return out, nil
	case string:
		return resolveString(v, lookup)
	default:
		return deepCopy(v), nil
	}
}

func resolveString(value string, lookup map[string]any) (any, error) {
	if m := fullPlaceholder.FindStringSubmatch(value); m != nil {
		v, ok := lookupRawPath(lookup, m[1])
		if !ok {
			return nil, InterpolationError{Name: "unresolved-variable", Message: fmt.Sprintf("Unresolved variable '%s'", m[1])}
		}
		return deepCopy(v), nil
	}
	var firstErr error
	out := embeddedPlaceholder.ReplaceAllStringFunc(value, func(token string) string {
		if firstErr != nil {
			return token
		}
		m := embeddedPlaceholder.FindStringSubmatch(token)
		v, ok := lookupRawPath(lookup, m[1])
		if !ok {
			firstErr = InterpolationError{Name: "unresolved-variable", Message: fmt.Sprintf("Unresolved variable '%s'", m[1])}
			return token
		}
		if v == nil || isComplexValue(v) {
			firstErr = InterpolationError{Name: "unresolved-variable", Message: fmt.Sprintf("Embedded interpolation requires scalar value for '%s'", m[1])}
			return token
		}
		return fmt.Sprint(v)
	})
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

type VariableLayers struct {
	Environment, SuiteDeclared, SuiteRuntime, TestDeclared, TestRuntime, StepDeclared, Inputs, Builtins map[string]any
}

func (l VariableLayers) AsLookup() map[string]any {
	m := map[string]any{}
	mergeInto(m, l.Environment)
	mergeInto(m, l.SuiteDeclared)
	mergeInto(m, l.SuiteRuntime)
	mergeInto(m, l.TestDeclared)
	mergeInto(m, l.TestRuntime)
	mergeInto(m, l.Builtins)
	mergeInto(m, l.StepDeclared)
	m["inputs"] = cloneMap(l.Inputs)
	return m
}
func mergeInto(dst, src map[string]any) {
	for k, v := range src {
		dst[k] = deepCopy(v)
	}
}

func BuildBuiltinVariables(seed int64, ctx map[string]any) map[string]any {
	rng := mathrand.New(mathrand.NewSource(seed))
	now := time.Now().UTC()
	alpha := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	rs := make([]byte, 12)
	for i := range rs {
		rs[i] = alpha[rng.Intn(len(alpha))]
	}
	out := map[string]any{"runId": newUUID(), "currentTimestamp": now.Format(time.RFC3339Nano), "currentIsoTimestamp": now.Format(time.RFC3339Nano), "currentUnixMs": now.UnixMilli(), "randomUuid": seededUUID(rng), "randomInt": rng.Intn(10000001), "randomString": string(rs)}
	for k, v := range ctx {
		out[k] = v
	}
	return out
}

// DeriveBuiltinSeed makes seeded random builtins deterministic for a specific
// suite/test/step/iteration while avoiding the old behavior where every step
// received the same randomInt/randomString for a given seed.
func DeriveBuiltinSeed(base int64, parts ...any) int64 {
	h := fnv.New64a()
	_, _ = fmt.Fprintf(h, "%d", base)
	for _, part := range parts {
		_, _ = fmt.Fprintf(h, "\x00%v", part)
	}
	return int64(h.Sum64())
}

func seededUUID(rng *mathrand.Rand) string {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(rng.Intn(256))
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b)
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}
func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	s := hex.EncodeToString(b)
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:32]
}

func RedactMapping(values map[string]any, sensitive map[string]bool) map[string]any {
	out := cloneMap(values)
	for k := range sensitive {
		if sensitive[k] {
			if _, ok := out[k]; ok {
				out[k] = RedactionValue
			}
		}
	}
	return out
}
