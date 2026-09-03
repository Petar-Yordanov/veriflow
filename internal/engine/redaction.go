package engine

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// Redactor tracks sensitive values discovered during a run and removes them from
// events, summaries and machine-readable reports. Values are matched by exact
// textual representation and by substring in larger strings (for URLs, headers,
// error messages, etc.).
type Redactor struct {
	mu     sync.RWMutex
	values map[string]struct{}
}

func NewRedactor() *Redactor { return &Redactor{values: map[string]struct{}{}} }

func (r *Redactor) Add(value any) {
	if r == nil || value == nil {
		return
	}
	for _, s := range scalarStrings(value) {
		if s == "" {
			continue
		}
		r.mu.Lock()
		r.values[s] = struct{}{}
		r.mu.Unlock()
	}
}

func scalarStrings(value any) []string {
	out := []string{}
	switch v := value.(type) {
	case string:
		out = append(out, v)
	case []byte:
		out = append(out, string(v))
	case map[string]any:
		for _, vv := range v {
			out = append(out, scalarStrings(vv)...)
		}
	case []any:
		for _, vv := range v {
			out = append(out, scalarStrings(vv)...)
		}
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		out = append(out, fmt.Sprint(v))
	}
	return out
}

func (r *Redactor) hasExact(value string) bool {
	if r == nil || value == "" {
		return false
	}
	r.mu.RLock()
	_, ok := r.values[value]
	r.mu.RUnlock()
	return ok
}

func (r *Redactor) String(s string) string {
	if r == nil || s == "" {
		return s
	}
	r.mu.RLock()
	values := make([]string, 0, len(r.values))
	for v := range r.values {
		values = append(values, v)
	}
	r.mu.RUnlock()
	// Longest first prevents a shorter secret from partially obscuring a longer one.
	sort.Slice(values, func(i, j int) bool { return len(values[i]) > len(values[j]) })
	out := s
	for _, v := range values {
		variants := []string{v}
		if escaped := url.QueryEscape(v); escaped != v {
			variants = append(variants, escaped)
		}
		if escaped := url.PathEscape(v); escaped != v {
			variants = append(variants, escaped)
		}
		for _, secret := range variants {
			if secret == "" {
				continue
			}
			if len(v) >= 3 {
				out = strings.ReplaceAll(out, secret, RedactionValue)
				continue
			}

			// A one- or two-character secret cannot be safely substring-replaced
			// without corrupting arbitrary words (for example secret "a"). First
			// redact it when it is a distinct token. If it is embedded in a larger
			// value, conservatively redact the whole field instead of leaking it.
			redacted := replaceSecretToken(out, secret)
			if strings.Contains(redacted, secret) {
				return RedactionValue
			}
			out = redacted
		}
	}
	return out
}

func replaceSecretToken(s, secret string) string {
	if s == secret {
		return RedactionValue
	}
	if secret == "" {
		return s
	}
	var b strings.Builder
	start := 0
	for {
		rel := strings.Index(s[start:], secret)
		if rel < 0 {
			b.WriteString(s[start:])
			break
		}
		i := start + rel
		j := i + len(secret)
		leftOK := i == 0 || !isSecretTokenByte(s[i-1])
		rightOK := j == len(s) || !isSecretTokenByte(s[j])
		if leftOK && rightOK {
			b.WriteString(s[start:i])
			b.WriteString(RedactionValue)
			start = j
		} else {
			b.WriteString(s[start:j])
			start = j
		}
	}
	return b.String()
}

func isSecretTokenByte(b byte) bool {
	return b == '_' || (b >= '0' && b <= '9') || (b >= 'A' && b <= 'Z') || (b >= 'a' && b <= 'z')
}

func (r *Redactor) URL(raw string) string {
	if r == nil || raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return r.String(raw)
	}
	q := u.Query()
	for key, values := range q {
		for i, value := range values {
			values[i] = r.String(value)
		}
		q[key] = values
	}
	u.RawQuery = q.Encode()
	if u.User != nil {
		username := r.String(u.User.Username())
		if password, ok := u.User.Password(); ok {
			u.User = url.UserPassword(username, r.String(password))
		} else {
			u.User = url.User(username)
		}
	}
	u.Path = r.String(u.Path)
	return r.String(u.String())
}

func (r *Redactor) Any(v any) any {
	switch x := v.(type) {
	case string:
		return r.String(x)
	case bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		if r.hasExact(fmt.Sprint(x)) {
			return RedactionValue
		}
		return v
	case []byte:
		return []byte(r.String(string(x)))
	case map[string]any:
		m := make(map[string]any, len(x))
		for k, vv := range x {
			if isSensitiveHeader(k) {
				m[k] = RedactionValue
			} else if str, ok := vv.(string); ok && isURLField(k) {
				m[k] = r.URL(str)
			} else {
				m[k] = r.Any(vv)
			}
		}
		return m
	case map[string]string:
		m := make(map[string]string, len(x))
		for k, vv := range x {
			if isSensitiveHeader(k) {
				m[k] = RedactionValue
			} else {
				m[k] = r.String(vv)
			}
		}
		return m
	case []any:
		a := make([]any, len(x))
		for i := range x {
			a[i] = r.Any(x[i])
		}
		return a
	default:
		return v
	}
}

func isURLField(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	return n == "url" || n == "requesturl" || n == "request_url" || strings.HasSuffix(n, "url")
}

func isSensitiveHeader(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "authorization" || n == "proxy-authorization" || n == "cookie" || n == "set-cookie" {
		return true
	}
	return strings.Contains(n, "api-key") || strings.Contains(n, "apikey") || strings.Contains(n, "token") || strings.Contains(n, "secret")
}

func (r *Redactor) Event(e EngineEvent) EngineEvent {
	payload, _ := r.Any(e.Payload).(map[string]any)
	e.Payload = payload
	return e
}

func (r *Redactor) StepResult(s StepResult) StepResult {
	if s.RequestSummary != nil {
		s.RequestSummary.URL = r.URL(s.RequestSummary.URL)
		if h, ok := r.Any(s.RequestSummary.Headers).(map[string]any); ok {
			s.RequestSummary.Headers = h
		}
	}
	if s.ResponseSummary != nil {
		if h, ok := r.Any(s.ResponseSummary.Headers).(map[string]string); ok {
			s.ResponseSummary.Headers = h
		}
		s.ResponseSummary.BodyPreview = r.String(s.ResponseSummary.BodyPreview)
	}
	for i := range s.Assertions {
		s.Assertions[i].Expected = r.Any(s.Assertions[i].Expected)
		s.Assertions[i].Actual = r.Any(s.Assertions[i].Actual)
		s.Assertions[i].Message = r.String(s.Assertions[i].Message)
	}
	for i := range s.Extractions {
		if s.Extractions[i].Sensitive {
			s.Extractions[i].Value = RedactionValue
		} else {
			s.Extractions[i].Value = r.Any(s.Extractions[i].Value)
		}
		s.Extractions[i].Message = r.String(s.Extractions[i].Message)
	}
	s.Error = r.String(s.Error)
	return s
}

func (r *Redactor) SuiteResult(s SuiteResult) SuiteResult {
	s.Error = r.String(s.Error)
	for i := range s.BeforeAll {
		s.BeforeAll[i] = r.StepResult(s.BeforeAll[i])
	}
	for i := range s.Tests {
		s.Tests[i].Error = r.String(s.Tests[i].Error)
		for j := range s.Tests[i].BeforeEach {
			s.Tests[i].BeforeEach[j] = r.StepResult(s.Tests[i].BeforeEach[j])
		}
		for j := range s.Tests[i].Steps {
			s.Tests[i].Steps[j] = r.StepResult(s.Tests[i].Steps[j])
		}
		for j := range s.Tests[i].AfterEach {
			s.Tests[i].AfterEach[j] = r.StepResult(s.Tests[i].AfterEach[j])
		}
	}
	for i := range s.AfterAll {
		s.AfterAll[i] = r.StepResult(s.AfterAll[i])
	}
	for _, d := range s.Diagnostics {
		for k, v := range d {
			d[k] = r.Any(v)
		}
	}
	return s
}
