package engine

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/textproto"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

type SelectorResult struct {
	Value   any
	Missing bool
}

type selectorUnionItem struct {
	name    string
	index   int
	isIndex bool
}

type selectorToken struct {
	kind       string
	name       string
	index      int
	union      []selectorUnionItem
	sliceStart *int
	sliceEnd   *int
	sliceStep  int
	filter     string
}

func Select(document any, selector string) (SelectorResult, error) {
	tokens, err := tokenizeSelector(selector)
	if err != nil {
		return SelectorResult{}, err
	}
	current := []any{document}
	for _, t := range tokens {
		next := []any{}
		switch t.kind {
		case "prop":
			for _, item := range current {
				if m, ok := item.(map[string]any); ok {
					if v, exists := m[t.name]; exists {
						next = append(next, v)
					}
				}
			}
		case "index":
			for _, item := range current {
				if a, ok := item.([]any); ok {
					idx := normalizeSelectorIndex(t.index, len(a))
					if idx >= 0 && idx < len(a) {
						next = append(next, a[idx])
					}
				}
			}
		case "wildcard":
			for _, item := range current {
				next = append(next, selectorChildren(item)...)
			}
		case "recursiveProp":
			for _, item := range current {
				selectorRecursiveProperty(item, t.name, &next)
			}
		case "recursiveWildcard":
			for _, item := range current {
				selectorRecursiveWildcard(item, &next)
			}
		case "recursiveUnion":
			for _, item := range current {
				selectorRecursiveUnion(item, t.union, &next)
			}
		case "slice":
			for _, item := range current {
				if a, ok := item.([]any); ok {
					next = append(next, selectorSlice(a, t.sliceStart, t.sliceEnd, t.sliceStep)...)
				}
			}
		case "union":
			for _, item := range current {
				for _, u := range t.union {
					if u.isIndex {
						if a, ok := item.([]any); ok {
							idx := normalizeSelectorIndex(u.index, len(a))
							if idx >= 0 && idx < len(a) {
								next = append(next, a[idx])
							}
						}
					} else if m, ok := item.(map[string]any); ok {
						if v, exists := m[u.name]; exists {
							next = append(next, v)
						}
					}
				}
			}
		case "filter":
			for _, item := range current {
				candidates := selectorChildren(item)
				for _, candidate := range candidates {
					ok, evalErr := evalSelectorFilter(t.filter, candidate, document)
					if evalErr != nil {
						return SelectorResult{}, fmt.Errorf("invalid selector filter %q: %w", t.filter, evalErr)
					}
					if ok {
						next = append(next, candidate)
					}
				}
			}
		default:
			return SelectorResult{}, fmt.Errorf("unsupported selector token %q", t.kind)
		}
		current = next
	}
	if len(current) == 0 {
		return SelectorResult{Missing: true}, nil
	}
	if len(current) == 1 {
		return SelectorResult{Value: current[0]}, nil
	}
	return SelectorResult{Value: current}, nil
}

func selectorChildren(item any) []any {
	switch v := item.(type) {
	case []any:
		return append([]any{}, v...)
	case map[string]any:
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		out := make([]any, 0, len(keys))
		for _, key := range keys {
			out = append(out, v[key])
		}
		return out
	}
	return nil
}

func selectorRecursiveProperty(item any, name string, out *[]any) {
	switch v := item.(type) {
	case map[string]any:
		if value, ok := v[name]; ok {
			*out = append(*out, value)
		}
		for _, child := range selectorChildren(v) {
			selectorRecursiveProperty(child, name, out)
		}
	case []any:
		for _, child := range v {
			selectorRecursiveProperty(child, name, out)
		}
	}
}

func selectorRecursiveUnion(item any, union []selectorUnionItem, out *[]any) {
	switch v := item.(type) {
	case map[string]any:
		for _, entry := range union {
			if entry.isIndex {
				continue
			}
			if value, ok := v[entry.name]; ok {
				*out = append(*out, value)
			}
		}
		for _, child := range selectorChildren(v) {
			selectorRecursiveUnion(child, union, out)
		}
	case []any:
		for _, child := range v {
			selectorRecursiveUnion(child, union, out)
		}
	}
}

func selectorRecursiveWildcard(item any, out *[]any) {
	for _, child := range selectorChildren(item) {
		*out = append(*out, child)
		selectorRecursiveWildcard(child, out)
	}
}

func normalizeSelectorIndex(index, length int) int {
	if index < 0 {
		return length + index
	}
	return index
}

func selectorSlice(a []any, start, end *int, step int) []any {
	if step == 0 {
		return nil
	}
	if step > 0 {
		s := 0
		e := len(a)
		if start != nil {
			s = normalizeSelectorIndex(*start, len(a))
		}
		if end != nil {
			e = normalizeSelectorIndex(*end, len(a))
		}
		if s < 0 {
			s = 0
		}
		if s > len(a) {
			s = len(a)
		}
		if e < 0 {
			e = 0
		}
		if e > len(a) {
			e = len(a)
		}
		out := []any{}
		for i := s; i < e; i += step {
			out = append(out, a[i])
		}
		return out
	}
	s := len(a) - 1
	e := -1
	if start != nil {
		s = normalizeSelectorIndex(*start, len(a))
	}
	if end != nil {
		e = normalizeSelectorIndex(*end, len(a))
	}
	if s >= len(a) {
		s = len(a) - 1
	}
	out := []any{}
	for i := s; i > e && i >= 0 && i < len(a); i += step {
		out = append(out, a[i])
	}
	return out
}

// tokenizeSelector supports the stable Veriflow JSONPath dialect: child access,
// quoted properties, wildcards, negative indexes, unions, slices, recursive
// descent, and filters with comparisons/logical operators.
func tokenizeSelector(s string) ([]selectorToken, error) {
	if s == "$" {
		return nil, nil
	}
	if !strings.HasPrefix(s, "$") {
		return nil, fmt.Errorf("selector must start with '$': %s", s)
	}
	out := []selectorToken{}
	for i := 1; i < len(s); {
		switch s[i] {
		case '.':
			if i+1 < len(s) && s[i+1] == '.' {
				i += 2
				if i < len(s) && s[i] == '*' {
					out = append(out, selectorToken{kind: "recursiveWildcard"})
					i++
					continue
				}
				if i < len(s) && s[i] == '[' {
					end, err := selectorBracketEnd(s, i)
					if err != nil {
						return nil, fmt.Errorf("invalid selector %q: %w", s, err)
					}
					tok, err := parseSelectorBracket(strings.TrimSpace(s[i+1 : end]))
					if err != nil {
						return nil, fmt.Errorf("invalid selector %q: %w", s, err)
					}
					switch tok.kind {
					case "prop":
						out = append(out, selectorToken{kind: "recursiveProp", name: tok.name})
					case "wildcard":
						out = append(out, selectorToken{kind: "recursiveWildcard"})
					case "union":
						for _, entry := range tok.union {
							if entry.isIndex {
								return nil, fmt.Errorf("recursive unions support quoted properties only")
							}
						}
						out = append(out, selectorToken{kind: "recursiveUnion", union: tok.union})
					default:
						return nil, fmt.Errorf("recursive bracket selector supports quoted properties, quoted-property unions, or wildcard")
					}
					i = end + 1
					continue
				}
				start := i
				for i < len(s) && isSelectorIdentifierByte(s[i], i == start) {
					i++
				}
				if start == i {
					return nil, fmt.Errorf("invalid recursive selector: %s", s)
				}
				out = append(out, selectorToken{kind: "recursiveProp", name: s[start:i]})
				continue
			}
			i++
			if i < len(s) && s[i] == '*' {
				out = append(out, selectorToken{kind: "wildcard"})
				i++
				continue
			}
			start := i
			for i < len(s) && isSelectorIdentifierByte(s[i], i == start) {
				i++
			}
			if start == i {
				return nil, fmt.Errorf("invalid selector: %s", s)
			}
			out = append(out, selectorToken{kind: "prop", name: s[start:i]})
		case '[':
			end, err := selectorBracketEnd(s, i)
			if err != nil {
				return nil, fmt.Errorf("invalid selector %q: %w", s, err)
			}
			inside := strings.TrimSpace(s[i+1 : end])
			if inside == "" {
				return nil, fmt.Errorf("empty selector bracket")
			}
			tok, err := parseSelectorBracket(inside)
			if err != nil {
				return nil, fmt.Errorf("invalid selector %q: %w", s, err)
			}
			out = append(out, tok)
			i = end + 1
		default:
			return nil, fmt.Errorf("invalid selector near %q", s[i:])
		}
	}
	return out, nil
}

func parseSelectorBracket(inside string) (selectorToken, error) {
	if inside == "*" {
		return selectorToken{kind: "wildcard"}, nil
	}
	if strings.HasPrefix(inside, "?(") && strings.HasSuffix(inside, ")") {
		expr := strings.TrimSpace(inside[2 : len(inside)-1])
		if expr == "" {
			return selectorToken{}, fmt.Errorf("empty filter")
		}
		if _, err := evalSelectorFilter(expr, map[string]any{}, map[string]any{}); err != nil && !strings.Contains(err.Error(), "missing operand") {
			return selectorToken{}, err
		}
		return selectorToken{kind: "filter", filter: expr}, nil
	}
	if parts := splitSelectorTopLevel(inside, ','); len(parts) > 1 {
		items := make([]selectorUnionItem, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if len(p) >= 2 && (p[0] == '\'' || p[0] == '"') {
				name, err := parseSelectorQuotedProperty(p)
				if err != nil {
					return selectorToken{}, err
				}
				items = append(items, selectorUnionItem{name: name})
				continue
			}
			idx, err := strconv.Atoi(p)
			if err != nil {
				return selectorToken{}, fmt.Errorf("union entries must be quoted properties or integer indexes")
			}
			items = append(items, selectorUnionItem{index: idx, isIndex: true})
		}
		return selectorToken{kind: "union", union: items}, nil
	}
	if strings.Contains(inside, ":") {
		parts := strings.Split(inside, ":")
		if len(parts) < 2 || len(parts) > 3 {
			return selectorToken{}, fmt.Errorf("invalid slice")
		}
		var start, end *int
		parseOptional := func(raw string) (*int, error) {
			raw = strings.TrimSpace(raw)
			if raw == "" {
				return nil, nil
			}
			v, err := strconv.Atoi(raw)
			if err != nil {
				return nil, err
			}
			return &v, nil
		}
		var err error
		if start, err = parseOptional(parts[0]); err != nil {
			return selectorToken{}, fmt.Errorf("invalid slice start")
		}
		if end, err = parseOptional(parts[1]); err != nil {
			return selectorToken{}, fmt.Errorf("invalid slice end")
		}
		step := 1
		if len(parts) == 3 && strings.TrimSpace(parts[2]) != "" {
			step, err = strconv.Atoi(strings.TrimSpace(parts[2]))
			if err != nil || step == 0 {
				return selectorToken{}, fmt.Errorf("slice step must be a non-zero integer")
			}
		}
		return selectorToken{kind: "slice", sliceStart: start, sliceEnd: end, sliceStep: step}, nil
	}
	if len(inside) >= 2 && (inside[0] == '\'' || inside[0] == '"') {
		name, err := parseSelectorQuotedProperty(inside)
		if err != nil {
			return selectorToken{}, err
		}
		return selectorToken{kind: "prop", name: name}, nil
	}
	idx, err := strconv.Atoi(inside)
	if err != nil {
		return selectorToken{}, fmt.Errorf("expected property, index, union, slice, wildcard, or filter")
	}
	return selectorToken{kind: "index", index: idx}, nil
}

func isSelectorIdentifierByte(b byte, first bool) bool {
	if b == '_' || b == '$' || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z') {
		return true
	}
	return !first && (b == '-' || (b >= '0' && b <= '9'))
}

func selectorBracketEnd(s string, start int) (int, error) {
	quote := byte(0)
	escaped := false
	parenDepth := 0
	nestedBracket := 0
	for i := start + 1; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		switch c {
		case '(':
			parenDepth++
		case ')':
			if parenDepth > 0 {
				parenDepth--
			}
		case '[':
			nestedBracket++
		case ']':
			if nestedBracket > 0 {
				nestedBracket--
				continue
			}
			if parenDepth == 0 {
				return i, nil
			}
		}
	}
	return 0, fmt.Errorf("unterminated selector bracket")
}

func parseSelectorQuotedProperty(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) < 2 {
		return "", fmt.Errorf("invalid quoted selector property")
	}
	quote := raw[0]
	if raw[len(raw)-1] != quote {
		return "", fmt.Errorf("unterminated quoted selector property")
	}
	if quote == '"' {
		return strconv.Unquote(raw)
	}
	inner := raw[1 : len(raw)-1]
	var b strings.Builder
	escaped := false
	for i := 0; i < len(inner); i++ {
		c := inner[i]
		if escaped {
			if c != '\\' && c != '\'' {
				return "", fmt.Errorf("unsupported escape")
			}
			b.WriteByte(c)
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		b.WriteByte(c)
	}
	if escaped {
		return "", fmt.Errorf("unterminated escape")
	}
	return b.String(), nil
}

func splitSelectorTopLevel(s string, sep byte) []string {
	parts := []string{}
	start := 0
	quote := byte(0)
	escaped := false
	parens := 0
	brackets := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		switch c {
		case '(':
			parens++
		case ')':
			parens--
		case '[':
			brackets++
		case ']':
			brackets--
		}
		if c == sep && parens == 0 && brackets == 0 {
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	return append(parts, s[start:])
}

func evalSelectorFilter(expr string, item, root any) (bool, error) {
	expr = strings.TrimSpace(expr)
	for strings.HasPrefix(expr, "(") && strings.HasSuffix(expr, ")") && selectorOuterParensWrap(expr) {
		expr = strings.TrimSpace(expr[1 : len(expr)-1])
	}
	if parts := splitSelectorOperator(expr, "||"); len(parts) > 1 {
		for _, p := range parts {
			ok, err := evalSelectorFilter(p, item, root)
			if err != nil {
				return false, err
			}
			if ok {
				return true, nil
			}
		}
		return false, nil
	}
	if parts := splitSelectorOperator(expr, "&&"); len(parts) > 1 {
		for _, p := range parts {
			ok, err := evalSelectorFilter(p, item, root)
			if err != nil || !ok {
				return false, err
			}
		}
		return true, nil
	}
	if strings.HasPrefix(expr, "!") {
		ok, err := evalSelectorFilter(strings.TrimSpace(expr[1:]), item, root)
		return !ok, err
	}
	for _, op := range []string{"=~", "==", "!=", ">=", "<=", ">", "<"} {
		if idx := findSelectorTopLevelOperator(expr, op); idx >= 0 {
			left, lmissing, err := evalSelectorOperand(strings.TrimSpace(expr[:idx]), item, root)
			if err != nil {
				return false, err
			}
			rightRaw := strings.TrimSpace(expr[idx+len(op):])
			if op == "=~" {
				pattern, flags, err := parseSelectorRegex(rightRaw)
				if err != nil {
					return false, err
				}
				if lmissing {
					return false, nil
				}
				if flags != "" {
					pattern = "(?" + flags + ")" + pattern
				}
				re, err := regexp.Compile(pattern)
				if err != nil {
					return false, err
				}
				return re.MatchString(fmt.Sprint(left)), nil
			}
			right, _, err := evalSelectorOperand(rightRaw, item, root)
			if err != nil {
				return false, err
			}
			if lmissing {
				return op == "!=" && right != nil, nil
			}
			return compareSelectorValues(left, right, op), nil
		}
	}
	v, missing, err := evalSelectorOperand(expr, item, root)
	if err != nil {
		return false, err
	}
	if missing {
		return false, nil
	}
	return selectorTruthy(v), nil
}

func evalSelectorOperand(raw string, item, root any) (any, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, true, fmt.Errorf("missing operand")
	}
	if raw == "@" {
		return item, false, nil
	}
	if strings.HasPrefix(raw, "@.") || strings.HasPrefix(raw, "@[") {
		r, err := Select(item, "$"+raw[1:])
		if err != nil {
			return nil, true, err
		}
		return r.Value, r.Missing, nil
	}
	if raw == "$" {
		return root, false, nil
	}
	if strings.HasPrefix(raw, "$.") || strings.HasPrefix(raw, "$[") {
		r, err := Select(root, raw)
		if err != nil {
			return nil, true, err
		}
		return r.Value, r.Missing, nil
	}
	if raw == "true" {
		return true, false, nil
	}
	if raw == "false" {
		return false, false, nil
	}
	if raw == "null" {
		return nil, false, nil
	}
	if len(raw) >= 2 && (raw[0] == '\'' || raw[0] == '"') {
		v, err := parseSelectorQuotedProperty(raw)
		return v, false, err
	}
	if n, err := strconv.ParseFloat(raw, 64); err == nil {
		return n, false, nil
	}
	return nil, true, fmt.Errorf("unsupported filter operand %q", raw)
}

func compareSelectorValues(a, b any, op string) bool {
	if op == "==" {
		return valuesEqual(a, b)
	}
	if op == "!=" {
		return !valuesEqual(a, b)
	}
	af, aok := asFloat(a)
	bf, bok := asFloat(b)
	if aok && bok {
		switch op {
		case ">":
			return af > bf
		case ">=":
			return af >= bf
		case "<":
			return af < bf
		case "<=":
			return af <= bf
		}
	}
	as, aok := a.(string)
	bs, bok := b.(string)
	if aok && bok {
		switch op {
		case ">":
			return as > bs
		case ">=":
			return as >= bs
		case "<":
			return as < bs
		case "<=":
			return as <= bs
		}
	}
	return false
}

func selectorTruthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	default:
		return true
	}
}
func selectorOuterParensWrap(s string) bool {
	depth := 0
	quote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote && (i == 0 || s[i-1] != '\\') {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		if c == '(' {
			depth++
		}
		if c == ')' {
			depth--
			if depth == 0 && i != len(s)-1 {
				return false
			}
		}
	}
	return depth == 0
}
func splitSelectorOperator(s, op string) []string {
	parts := []string{}
	start := 0
	for {
		idx := findSelectorTopLevelOperator(s[start:], op)
		if idx < 0 {
			break
		}
		idx += start
		parts = append(parts, s[start:idx])
		start = idx + len(op)
	}
	if len(parts) == 0 {
		return []string{s}
	}
	return append(parts, s[start:])
}
func findSelectorTopLevelOperator(s, op string) int {
	quote := byte(0)
	escaped := false
	parens := 0
	brackets := 0
	for i := 0; i+len(op) <= len(s); i++ {
		c := s[i]
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				continue
			}
			if c == quote {
				quote = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quote = c
			continue
		}
		switch c {
		case '(':
			parens++
		case ')':
			parens--
		case '[':
			brackets++
		case ']':
			brackets--
		}
		if parens == 0 && brackets == 0 && strings.HasPrefix(s[i:], op) {
			return i
		}
	}
	return -1
}
func parseSelectorRegex(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) < 2 || raw[0] != '/' {
		return "", "", fmt.Errorf("regex filter expects /pattern/flags")
	}
	escaped := false
	for i := 1; i < len(raw); i++ {
		if escaped {
			escaped = false
			continue
		}
		if raw[i] == '\\' {
			escaped = true
			continue
		}
		if raw[i] == '/' {
			flags := raw[i+1:]
			seen := map[byte]bool{}
			for j := 0; j < len(flags); j++ {
				f := flags[j]
				if f != 'i' && f != 'm' && f != 's' {
					return "", "", fmt.Errorf("unsupported regex flag %q", f)
				}
				if seen[f] {
					return "", "", fmt.Errorf("duplicate regex flag %q", f)
				}
				seen[f] = true
			}
			return raw[1:i], flags, nil
		}
	}
	return "", "", fmt.Errorf("unterminated regex")
}

func IsSupportedJSONPath(s string) bool { _, err := tokenizeSelector(s); return err == nil }

type PreparedRequest struct {
	Method, URL     string
	Headers         map[string]string
	Content         []byte
	SensitiveValues []any
}

func PrepareRequest(request RequestSpec, lookup map[string]any, projectRoot string) (PreparedRequest, error) {
	raw := map[string]any{"method": request.Method, "url": request.URL, "baseUrl": request.BaseURL, "path": request.Path, "pathParams": request.PathParams, "query": request.Query, "headers": request.Headers, "cookies": request.Cookies, "auth": authToMap(request.Auth), "body": request.Body, "bodyRaw": request.BodyRaw, "bodyFile": request.BodyFile, "bodyFileMode": request.BodyFileMode, "form": request.Form, "multipart": request.Multipart}
	resolvedAny, err := ResolveData(raw, lookup)
	if err != nil {
		return PreparedRequest{}, err
	}
	r := resolvedAny.(map[string]any)
	method := strings.ToUpper(asString(r["method"]))
	u, err := buildURL(r, request.PathParamEncoding.Enabled)
	if err != nil {
		return PreparedRequest{}, err
	}
	headers := map[string]string{}
	for k, v := range asMap(r["headers"]) {
		headers[k] = fmt.Sprint(v)
	}
	if cookies := asMap(r["cookies"]); len(cookies) > 0 {
		names := make([]string, 0, len(cookies))
		for name := range cookies {
			names = append(names, name)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for _, name := range names {
			if cookies[name] == nil {
				return PreparedRequest{}, fmt.Errorf("cookie %q cannot be null", name)
			}
			parts = append(parts, (&http.Cookie{Name: name, Value: fmt.Sprint(cookies[name])}).String())
		}
		if existing := headers["Cookie"]; existing != "" {
			parts = append([]string{existing}, parts...)
		}
		headers["Cookie"] = strings.Join(parts, "; ")
	}
	sensitiveValues := []any{}
	if auth := asMap(r["auth"]); len(auth) > 0 {
		authType := strings.ToLower(strings.TrimSpace(asString(auth["type"])))
		switch authType {
		case "basic":
			username, password := asString(auth["username"]), asString(auth["password"])
			encoded := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
			headers["Authorization"] = "Basic " + encoded
			sensitiveValues = append(sensitiveValues, password, encoded)
		case "bearer":
			token := asString(auth["token"])
			headers["Authorization"] = "Bearer " + token
			sensitiveValues = append(sensitiveValues, token)
		case "apikey", "api-key":
			name, value, where := asString(auth["name"]), asString(auth["value"]), strings.ToLower(asString(auth["in"]))
			if where == "" {
				where = "header"
			}
			if where == "query" {
				parsed, parseErr := url.Parse(u)
				if parseErr != nil {
					return PreparedRequest{}, parseErr
				}
				q := parsed.Query()
				q.Set(name, value)
				parsed.RawQuery = q.Encode()
				u = parsed.String()
			} else {
				headers[name] = value
			}
			sensitiveValues = append(sensitiveValues, value)
		}
	}
	var content []byte
	bodyModes := 0
	if r["body"] != nil {
		bodyModes++
	}
	if asString(r["bodyRaw"]) != "" {
		bodyModes++
	}
	if asString(r["bodyFile"]) != "" {
		bodyModes++
	}
	if len(asMap(r["form"])) > 0 {
		bodyModes++
	}
	if len(asMap(r["multipart"])) > 0 {
		bodyModes++
	}
	if bodyModes > 1 {
		return PreparedRequest{}, fmt.Errorf("request defines conflicting body modes")
	}
	if r["body"] != nil {
		content, err = json.Marshal(r["body"])
		if err != nil {
			return PreparedRequest{}, err
		}
		if _, ok := headers["Content-Type"]; !ok {
			headers["Content-Type"] = "application/json"
		}
	} else if s := asString(r["bodyRaw"]); s != "" {
		content = []byte(s)
	} else if file := asString(r["bodyFile"]); file != "" {
		content, err = readPayloadFile(projectRoot, file)
		if err != nil {
			return PreparedRequest{}, err
		}
		mode := strings.ToLower(strings.TrimSpace(asString(r["bodyFileMode"])))
		switch mode {
		case "json":
			var v any
			dec := json.NewDecoder(bytes.NewReader(content))
			dec.UseNumber()
			if err := dec.Decode(&v); err != nil {
				return PreparedRequest{}, fmt.Errorf("bodyFile JSON: %w", err)
			}
			v = normalizeDecodedJSON(v)
			v, err = ResolveData(v, lookup)
			if err != nil {
				return PreparedRequest{}, fmt.Errorf("bodyFile JSON interpolation: %w", err)
			}
			content, err = json.Marshal(v)
			if err != nil {
				return PreparedRequest{}, err
			}
			if _, ok := headers["Content-Type"]; !ok {
				headers["Content-Type"] = "application/json"
			}
		case "text":
			resolved, resolveErr := resolveString(string(content), lookup)
			if resolveErr != nil {
				return PreparedRequest{}, fmt.Errorf("bodyFile text interpolation: %w", resolveErr)
			}
			content = []byte(fmt.Sprint(resolved))
		case "", "binary":
		default:
			return PreparedRequest{}, fmt.Errorf("unsupported bodyFileMode %q; use binary, text, or json", mode)
		}
	} else if form := asMap(r["form"]); len(form) > 0 {
		vals := url.Values{}
		for k, v := range form {
			addURLValues(vals, k, v)
		}
		content = []byte(vals.Encode())
		if _, ok := headers["Content-Type"]; !ok {
			headers["Content-Type"] = "application/x-www-form-urlencoded"
		}
	} else if mp := asMap(r["multipart"]); len(mp) > 0 {
		var b bytes.Buffer
		w := multipart.NewWriter(&b)
		keys := make([]string, 0, len(mp))
		for key := range mp {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := mp[k]
			if fm := asMap(v); fm != nil && asString(fm["file"]) != "" {
				fileName := asString(fm["filename"])
				if fileName == "" {
					fileName = filepath.Base(asString(fm["file"]))
				}
				contentType := asString(fm["contentType"])
				if contentType == "" {
					contentType = "application/octet-stream"
				}
				data, e := readPayloadFile(projectRoot, asString(fm["file"]))
				if e != nil {
					return PreparedRequest{}, e
				}
				h := make(textproto.MIMEHeader)
				h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%s; filename=%s`, quoteMultipartParam(k), quoteMultipartParam(fileName)))
				h.Set("Content-Type", contentType)
				part, e := w.CreatePart(h)
				if e != nil {
					return PreparedRequest{}, e
				}
				if _, e = part.Write(data); e != nil {
					return PreparedRequest{}, e
				}
			} else {
				values := flattenScalarValues(v)
				for _, value := range values {
					if e := w.WriteField(k, value); e != nil {
						return PreparedRequest{}, e
					}
				}
			}
		}
		if e := w.Close(); e != nil {
			return PreparedRequest{}, e
		}
		content = b.Bytes()
		headers["Content-Type"] = w.FormDataContentType()
	}
	return PreparedRequest{Method: method, URL: u, Headers: headers, Content: content, SensitiveValues: sensitiveValues}, nil
}

func authToMap(auth *AuthSpec) map[string]any {
	if auth == nil {
		return nil
	}
	return map[string]any{"type": auth.Type, "username": auth.Username, "password": auth.Password, "token": auth.Token, "name": auth.Name, "value": auth.Value, "in": auth.In}
}

func readPayloadFile(projectRoot, file string) ([]byte, error) {
	p := file
	if !filepath.IsAbs(p) {
		p = filepath.Join(projectRoot, p)
	}
	abs, err := ensureExistingPathWithinRoot(projectRoot, p, "bodyFile")
	if err != nil {
		if !strings.Contains(err.Error(), file) {
			return nil, fmt.Errorf("bodyFile %s: %w", file, err)
		}
		return nil, err
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, fmt.Errorf("read bodyFile %s: %w", file, err)
	}
	return data, nil
}
func buildURL(r map[string]any, encodeParams bool) (string, error) {
	var u string
	if asString(r["url"]) != "" {
		u = asString(r["url"])
	} else {
		base := asString(r["baseUrl"])
		path := asString(r["path"])
		if base == "" || path == "" {
			return "", fmt.Errorf("request must define url or both baseUrl and path")
		}
		for k, v := range asMap(r["pathParams"]) {
			s := fmt.Sprint(v)
			if encodeParams {
				s = url.PathEscape(s)
			}
			path = strings.ReplaceAll(path, "{"+k+"}", s)
		}
		u = base + path
	}
	q := asMap(r["query"])
	if len(q) > 0 {
		vals := url.Values{}
		for k, v := range q {
			if v == nil {
				return "", fmt.Errorf("query parameter %q cannot be null", k)
			}
			addURLValues(vals, k, v)
		}
		sep := "?"
		if strings.Contains(u, "?") {
			sep = "&"
		}
		u += sep + vals.Encode()
	}
	return u, nil
}

func addURLValues(values url.Values, key string, value any) {
	if list, ok := value.([]any); ok {
		for _, item := range list {
			values.Add(key, fmt.Sprint(item))
		}
		return
	}
	values.Add(key, fmt.Sprint(value))
}

func flattenScalarValues(value any) []string {
	if list, ok := value.([]any); ok {
		out := make([]string, 0, len(list))
		for _, item := range list {
			out = append(out, fmt.Sprint(item))
		}
		return out
	}
	return []string{fmt.Sprint(value)}
}

func quoteMultipartParam(value string) string {
	// mime.FormatMediaType performs the quoting/escaping required by RFC 2045
	// and RFC 7578. Strip the synthetic media type prefix back off.
	formatted := mime.FormatMediaType("x", map[string]string{"v": value})
	const prefix = "x; v="
	if strings.HasPrefix(formatted, prefix) {
		return strings.TrimPrefix(formatted, prefix)
	}
	return strconv.Quote(value)
}

type HTTPResponse struct {
	StatusCode int
	Headers    map[string]string
	Cookies    map[string]string
	Body       []byte
	JSON       any
}

func (r HTTPResponse) Text() string { return string(r.Body) }

type ResponseTooLargeError struct {
	Limit int64
}

func (e *ResponseTooLargeError) Error() string {
	return fmt.Sprintf("response body exceeds maximum size of %d bytes", e.Limit)
}

type SendHTTPOptions struct {
	TimeoutMS        int
	FollowRedirects  *bool
	MaxResponseBytes int64
}

const DefaultMaxResponseBytes int64 = 10 << 20 // 10 MiB

type HTTPClientConfig struct {
	CAFile                  string
	ClientCertFile          string
	ClientKeyFile           string
	ProxyURL                string
	InsecureSkipVerify      bool
	EnableCookieJar         bool
	ConnectTimeoutMS        int
	TLSHandshakeTimeoutMS   int
	ResponseHeaderTimeoutMS int
}

func durationMS(ms int) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func NewHTTPClient(config HTTPClientConfig) (*http.Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: config.InsecureSkipVerify} //nolint:gosec -- explicit opt-in CLI setting
	if config.CAFile != "" {
		pem, err := os.ReadFile(config.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read CA file: %w", err)
		}
		roots, err := x509.SystemCertPool()
		if err != nil || roots == nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA file contains no valid PEM certificates: %s", config.CAFile)
		}
		tlsConfig.RootCAs = roots
	}
	if (config.ClientCertFile == "") != (config.ClientKeyFile == "") {
		return nil, fmt.Errorf("client certificate and key must be provided together")
	}
	if config.ClientCertFile != "" {
		cert, err := tls.LoadX509KeyPair(config.ClientCertFile, config.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{cert}
	}
	proxy := http.ProxyFromEnvironment
	if config.ProxyURL != "" {
		p, err := url.Parse(config.ProxyURL)
		if err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
			return nil, fmt.Errorf("invalid proxy URL %q", config.ProxyURL)
		}
		proxy = http.ProxyURL(p)
	}
	connectTimeout := durationMS(config.ConnectTimeoutMS)
	tlsHandshakeTimeout := durationMS(config.TLSHandshakeTimeoutMS)
	responseHeaderTimeout := durationMS(config.ResponseHeaderTimeoutMS)
	transport := &http.Transport{
		Proxy:                 proxy,
		DialContext:           (&net.Dialer{Timeout: connectTimeout, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   tlsHandshakeTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
		ExpectContinueTimeout: time.Second,
		TLSClientConfig:       tlsConfig,
	}
	client := &http.Client{Transport: transport}
	if config.EnableCookieJar {
		jar, err := cookiejar.New(nil)
		if err != nil {
			return nil, fmt.Errorf("create cookie jar: %w", err)
		}
		client.Jar = jar
	}
	return client, nil
}

func defaultHTTPClient() *http.Client {
	client, err := NewHTTPClient(HTTPClientConfig{})
	if err != nil {
		return &http.Client{}
	}
	return client
}

func SendHTTP(ctx context.Context, client *http.Client, prepared PreparedRequest, timeoutMS int, follow *bool) (HTTPResponse, error) {
	return SendHTTPWithOptions(ctx, client, prepared, SendHTTPOptions{TimeoutMS: timeoutMS, FollowRedirects: follow, MaxResponseBytes: DefaultMaxResponseBytes})
}

func SendHTTPWithOptions(ctx context.Context, client *http.Client, prepared PreparedRequest, options SendHTTPOptions) (HTTPResponse, error) {
	c := client
	if c == nil {
		c = defaultHTTPClient()
	}
	copyClient := *c
	if options.TimeoutMS > 0 {
		copyClient.Timeout = time.Duration(options.TimeoutMS) * time.Millisecond
	}
	if options.FollowRedirects != nil && !*options.FollowRedirects {
		copyClient.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	}
	req, err := http.NewRequestWithContext(ctx, prepared.Method, prepared.URL, bytes.NewReader(prepared.Content))
	if err != nil {
		return HTTPResponse{}, err
	}
	for k, v := range prepared.Headers {
		req.Header.Set(k, v)
	}
	resp, err := copyClient.Do(req)
	if err != nil {
		return HTTPResponse{}, err
	}
	defer resp.Body.Close()
	limit := options.MaxResponseBytes
	if limit <= 0 {
		limit = DefaultMaxResponseBytes
	}
	reader := io.LimitReader(resp.Body, limit+1)
	body, err := io.ReadAll(reader)
	if err != nil {
		return HTTPResponse{}, err
	}
	if int64(len(body)) > limit {
		return HTTPResponse{}, &ResponseTooLargeError{Limit: limit}
	}
	hdr := map[string]string{}
	for k, v := range resp.Header {
		hdr[k] = strings.Join(v, ", ")
	}
	cookies := map[string]string{}
	for _, cookie := range resp.Cookies() {
		cookies[cookie.Name] = cookie.Value
	}
	var jsonBody any
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	if err := dec.Decode(&jsonBody); err == nil {
		jsonBody = normalizeDecodedJSON(jsonBody)
	}
	return HTTPResponse{StatusCode: resp.StatusCode, Headers: hdr, Cookies: cookies, Body: body, JSON: jsonBody}, nil
}

func IsTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if ne, ok := err.(net.Error); ok && ne.Timeout() {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "timeout") || strings.Contains(strings.ToLower(err.Error()), "deadline exceeded")
}

func normalizeDecodedJSON(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		if f, err := x.Float64(); err == nil {
			return f
		}
		return x.String()
	case map[string]any:
		for k, vv := range x {
			x[k] = normalizeDecodedJSON(vv)
		}
		return x
	case []any:
		for i := range x {
			x[i] = normalizeDecodedJSON(x[i])
		}
		return x
	default:
		return v
	}
}

type AssertionOutcome struct {
	Passed      bool
	Evaluations []AssertionEvaluation
}

func EvaluateAssertions(expect *ExpectationSpec, response HTTPResponse, timing map[string]float64, jsonBody any, lookup map[string]any) (AssertionOutcome, error) {
	evals := []AssertionEvaluation{}
	if expect == nil {
		return AssertionOutcome{Passed: true}, nil
	}
	if expect.Status != nil {
		resolved, err := ResolveData(expect.Status, lookup)
		if err != nil {
			return AssertionOutcome{}, err
		}
		allowed := []int{}
		switch x := resolved.(type) {
		case []any:
			for _, v := range x {
				allowed = append(allowed, asInt(v))
			}
		default:
			allowed = []int{asInt(x)}
		}
		passed := containsInt(allowed, response.StatusCode)
		evals = append(evals, newEval("status", "in", allowed, response.StatusCode, passed))
	}
	if expect.Body != nil {
		e, err := evaluateClause(*expect.Body, jsonBody, lookup)
		if err != nil {
			return AssertionOutcome{}, err
		}
		evals = append(evals, e...)
	}
	for name, h := range expect.Headers {
		actual, found := headerLookupValue(response.Headers, name)
		var value any = actual
		if !found {
			value = nil
		}
		e, err := evaluateOps("header:"+name, value, h.Operators, !found, lookup)
		if err != nil {
			return AssertionOutcome{}, err
		}
		evals = append(evals, e...)
	}
	if expect.Text != nil {
		e, err := evaluateOps("text", string(response.Body), expect.Text.Operators, false, lookup)
		if err != nil {
			return AssertionOutcome{}, err
		}
		evals = append(evals, e...)
	}
	if expect.Binary != nil {
		e, err := evaluateOps("binary", response.Body, expect.Binary.Operators, false, lookup)
		if err != nil {
			return AssertionOutcome{}, err
		}
		evals = append(evals, e...)
	}
	if expect.Performance != nil {
		for metric, ops := range expect.Performance.Metrics {
			e, err := evaluateOps("performance:"+metric, timing[metric], ops, false, lookup)
			if err != nil {
				return AssertionOutcome{}, err
			}
			evals = append(evals, e...)
		}
	}
	passed := true
	for _, e := range evals {
		if !e.Passed {
			passed = false
			break
		}
	}
	return AssertionOutcome{Passed: passed, Evaluations: evals}, nil
}
func evaluateClause(c AssertionClause, jsonBody any, lookup map[string]any) ([]AssertionEvaluation, error) {
	evals := []AssertionEvaluation{}
	if c.Path != "" {
		r, err := Select(jsonBody, c.Path)
		if err != nil {
			return nil, err
		}
		actual := r.Value
		missing := r.Missing
		if c.ElementField != "" && !missing {
			values := []any{}
			for _, item := range asSlice(actual) {
				if m := asMap(item); m != nil {
					if v, ok := m[c.ElementField]; ok {
						values = append(values, v)
					}
				}
			}
			actual = values
			missing = len(values) == 0
		}
		e, err := evaluateOps(c.Path, actual, c.Operators, missing, lookup)
		if err != nil {
			return nil, err
		}
		evals = append(evals, e...)
	}
	for _, ch := range c.And {
		e, err := evaluateClause(ch, jsonBody, lookup)
		if err != nil {
			return nil, err
		}
		evals = append(evals, e...)
	}
	if len(c.Or) > 0 {
		anyPassed := false
		branches := make([][]AssertionEvaluation, 0, len(c.Or))
		for _, ch := range c.Or {
			branch, err := evaluateClause(ch, jsonBody, lookup)
			if err != nil {
				return nil, err
			}
			branches = append(branches, branch)
			ok := true
			for _, e := range branch {
				if !e.Passed {
					ok = false
					break
				}
			}
			if ok {
				anyPassed = true
				break
			}
		}
		evals = append(evals, newEval("or", "or", true, anyPassed, anyPassed))
		if !anyPassed {
			// Preserve every failed branch when OR fails. A generic "no branch
			// matched" message alone is not enough to diagnose a pipeline failure.
			for branchIndex, branch := range branches {
				for _, evaluation := range branch {
					if evaluation.Passed {
						continue
					}
					evaluation.Target = fmt.Sprintf("or[%d].%s", branchIndex, evaluation.Target)
					evals = append(evals, evaluation)
				}
			}
		}
	}
	return evals, nil
}
func evaluateOps(target string, actual any, ops map[string]any, missing bool, lookup map[string]any) ([]AssertionEvaluation, error) {
	out := []AssertionEvaluation{}
	keys := make([]string, 0, len(ops))
	for k := range ops {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, op := range keys {
		expected, err := ResolveData(ops[op], lookup)
		if err != nil {
			return nil, err
		}
		passed := false
		switch op {
		case "exists":
			passed = asBool(expected) != missing
		case "isNull":
			if asBool(expected) {
				passed = actual == nil && !missing
			} else {
				passed = actual != nil
			}
		case "type":
			passed = typeName(actual) == asString(expected)
		case "equals":
			passed = valuesEqual(actual, expected)
		case "notEquals":
			passed = !valuesEqual(actual, expected)
		case "equalsIgnoreCase":
			a, aok := actual.(string)
			b, bok := expected.(string)
			passed = aok && bok && strings.EqualFold(a, b)
		case "in":
			passed = valueIn(actual, expected)
		case "notIn":
			passed = !valueIn(actual, expected)
		case "matches":
			if s, ok := actual.(string); ok {
				re, e := regexp.Compile(asString(expected))
				passed = e == nil && re.MatchString(s)
			}
		case "contains":
			passed = containsValue(actual, expected)
		case "notContains":
			passed = !containsValue(actual, expected)
		case "startsWith":
			a, ok := actual.(string)
			passed = ok && strings.HasPrefix(a, asString(expected))
		case "endsWith":
			a, ok := actual.(string)
			passed = ok && strings.HasSuffix(a, asString(expected))
		case "count", "length", "minCount", "minLength", "maxCount", "maxLength":
			length, ok := valueLength(actual)
			if ok {
				switch op {
				case "count", "length":
					passed = length == asInt(expected)
				case "minCount", "minLength":
					passed = length >= asInt(expected)
				case "maxCount", "maxLength":
					passed = length <= asInt(expected)
				}
			}
		case "unique":
			if a, ok := actual.([]any); ok {
				seen := map[string]bool{}
				isUnique := true
				for _, v := range a {
					b, _ := json.Marshal(v)
					k := string(b)
					if seen[k] {
						isUnique = false
						break
					}
					seen[k] = true
				}
				passed = isUnique == asBool(expected)
			}
		case "greaterThan", "greaterThanOrEqual", "lessThan", "lessThanOrEqual":
			a, aok := asFloat(actual)
			b, bok := asFloat(expected)
			if aok && bok {
				switch op {
				case "greaterThan":
					passed = a > b
				case "greaterThanOrEqual":
					passed = a >= b
				case "lessThan":
					passed = a < b
				case "lessThanOrEqual":
					passed = a <= b
				}
			}
		case "sha256":
			var content []byte
			switch v := actual.(type) {
			case []byte:
				content = v
			case string:
				content = []byte(v)
			}
			if content != nil {
				sum := sha256.Sum256(content)
				passed = strings.EqualFold(hex.EncodeToString(sum[:]), asString(expected))
			}
		case "before", "after", "onOrBefore", "onOrAfter":
			a, e1 := parseTimeValue(actual)
			b, e2 := parseTimeValue(expected)
			if e1 == nil && e2 == nil {
				switch op {
				case "before":
					passed = a.Before(b)
				case "after":
					passed = a.After(b)
				case "onOrBefore":
					passed = a.Before(b) || a.Equal(b)
				case "onOrAfter":
					passed = a.After(b) || a.Equal(b)
				}
			}
		}
		out = append(out, newEval(target, op, expected, actual, passed))
	}
	return out, nil
}
func newEval(target, op string, expected, actual any, passed bool) AssertionEvaluation {
	e := AssertionEvaluation{Target: target, Operator: op, Expected: expected, Actual: actual, Passed: passed}
	if !passed {
		e.Message = fmt.Sprintf("Assertion failed for %s.%s", target, op)
	}
	return e
}
func valuesEqual(a, b any) bool {
	af, aok := asFloat(a)
	bf, bok := asFloat(b)
	if aok && bok {
		return math.Abs(af-bf) < 1e-12
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return bytes.Equal(aj, bj)
}
func valueIn(actual, expected any) bool {
	for _, v := range asSlice(expected) {
		if valuesEqual(actual, v) {
			return true
		}
	}
	return false
}
func containsValue(actual, expected any) bool {
	switch a := actual.(type) {
	case string:
		return strings.Contains(a, fmt.Sprint(expected))
	case []any:
		for _, v := range a {
			if valuesEqual(v, expected) {
				return true
			}
		}
	}
	return false
}
func valueLength(v any) (int, bool) {
	switch x := v.(type) {
	case string:
		return len([]rune(x)), true
	case []any:
		return len(x), true
	case map[string]any:
		return len(x), true
	case []byte:
		return len(x), true
	default:
		return 0, false
	}
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case int, int64, float64, json.Number:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}
func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case float64:
		return x, true
	case json.Number:
		f, e := x.Float64()
		return f, e == nil
	}
	return 0, false
}
func parseTimeValue(v any) (time.Time, error) {
	s, ok := v.(string)
	if !ok {
		return time.Time{}, fmt.Errorf("date/time assertions require string values")
	}
	return time.Parse(time.RFC3339, strings.ReplaceAll(s, "Z", "+00:00"))
}
func containsInt(a []int, v int) bool {
	for _, x := range a {
		if x == v {
			return true
		}
	}
	return false
}
func headerLookupValue(h map[string]string, name string) (string, bool) {
	for k, v := range h {
		if strings.EqualFold(k, name) {
			return v, true
		}
	}
	return "", false
}

func headerLookup(h map[string]string, name string) string {
	v, _ := headerLookupValue(h, name)
	return v
}

type ExtractionOutcome struct {
	Variables map[string]any
	Results   []ExtractionResult
	OK        bool
}

func ExtractValues(specs map[string]ExtractionSpec, requestDef *RequestDefinitionSpec, response HTTPResponse, lookup map[string]any) (ExtractionOutcome, error) {
	out := ExtractionOutcome{Variables: map[string]any{}, OK: true}
	names := make([]string, 0, len(specs))
	for k := range specs {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		spec := effectiveExtractionSpec(specs[name], requestDef)
		value, missing, err := extractOne(spec, requestDef, response, lookup)
		if err != nil {
			return out, fmt.Errorf("extraction %q: %w", name, err)
		}
		if missing {
			passed := !spec.Required
			out.OK = out.OK && passed
			res := ExtractionResult{Name: name, Scope: string(spec.Scope), Sensitive: spec.Sensitive, Missing: true, Passed: passed}
			if !passed {
				res.Message = fmt.Sprintf("Required extraction '%s' missing", name)
			}
			out.Results = append(out.Results, res)
			continue
		}
		out.Variables[name] = value
		out.Results = append(out.Results, ExtractionResult{Name: name, Scope: string(spec.Scope), Value: value, Sensitive: spec.Sensitive, Passed: true})
	}
	return out, nil
}

func effectiveExtractionSpec(spec ExtractionSpec, requestDef *RequestDefinitionSpec) ExtractionSpec {
	if spec.FromDefinition == "" || requestDef == nil {
		return spec
	}
	if output, ok := requestDef.Outputs[spec.FromDefinition]; ok {
		spec.Required = spec.Required || output.Required
		spec.Sensitive = spec.Sensitive || output.Sensitive
	}
	return spec
}

func extractOne(spec ExtractionSpec, requestDef *RequestDefinitionSpec, response HTTPResponse, lookup map[string]any) (any, bool, error) {
	if spec.FromDefinition != "" {
		if requestDef == nil {
			return nil, true, fmt.Errorf("fromDefinition requires a request definition")
		}
		def, ok := requestDef.Outputs[spec.FromDefinition]
		if !ok {
			return nil, true, fmt.Errorf("request output %q not found", spec.FromDefinition)
		}
		result, err := Select(response.JSON, def.Path)
		return result.Value, result.Missing, err
	}
	if spec.FromSelector != "" {
		result, err := Select(response.JSON, spec.FromSelector)
		return result.Value, result.Missing, err
	}
	if spec.FromHeader != "" {
		nameAny, err := resolveString(spec.FromHeader, lookup)
		if err != nil {
			return nil, true, err
		}
		name := fmt.Sprint(nameAny)
		value := headerLookup(response.Headers, name)
		return value, value == "", nil
	}
	if spec.FromCookie != "" {
		nameAny, err := resolveString(spec.FromCookie, lookup)
		if err != nil {
			return nil, true, err
		}
		value, ok := response.Cookies[fmt.Sprint(nameAny)]
		return value, !ok, nil
	}
	if spec.FromTextRegex != "" {
		patternAny, err := resolveString(spec.FromTextRegex, lookup)
		if err != nil {
			return nil, true, err
		}
		re, err := regexp.Compile(fmt.Sprint(patternAny))
		if err != nil {
			return nil, true, fmt.Errorf("invalid text regular expression: %w", err)
		}
		match := re.FindStringSubmatch(string(response.Body))
		if match == nil {
			return nil, true, nil
		}
		if len(match) > 1 {
			return match[1], false, nil
		}
		return match[0], false, nil
	}
	if spec.FromStatus {
		return response.StatusCode, false, nil
	}
	return nil, true, fmt.Errorf("no extraction source configured")
}
