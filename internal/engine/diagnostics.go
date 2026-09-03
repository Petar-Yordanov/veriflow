package engine

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Diagnostic codes are a stable machine-readable contract. The symbolic Name
// remains human-readable, while Code is suitable for CI integrations that must
// survive wording changes.
var diagnosticCodes = map[string]string{
	"yaml-parse-error":                           "VF1001",
	"yaml-duplicate-key":                         "VF1002",
	"yaml-depth-limit":                           "VF1003",
	"yaml-alias-limit":                           "VF1004",
	"invalid-utf8":                               "VF1005",
	"spec-too-large":                             "VF1006",
	"load-error":                                 "VF1007",
	"missing-format-version":                     "VF1008",
	"unsupported-format-version":                 "VF1010",
	"missing-kind":                               "VF1011",
	"unsupported-kind":                           "VF1012",
	"invalid-suite-kind":                         "VF1013",
	"unknown-field":                              "VF1101",
	"invalid-type":                               "VF1102",
	"missing-test-id":                            "VF1201",
	"duplicate-test-id":                          "VF1202",
	"duplicate-step-id":                          "VF1203",
	"duplicate-suite-id":                         "VF1204",
	"duplicate-request-id":                       "VF1205",
	"duplicate-environment-name":                 "VF1206",
	"duplicate-test-case":                        "VF1207",
	"invalid-test-case":                          "VF1208",
	"invalid-test-id":                            "VF1209",
	"step-shape":                                 "VF1210",
	"invalid-timeout":                            "VF1211",
	"invalid-wait":                               "VF1212",
	"invalid-repeat":                             "VF1213",
	"invalid-retry":                              "VF1214",
	"invalid-retry-backoff":                      "VF1215",
	"duplicate-expanded-test-id":                 "VF1216",
	"missing-request":                            "VF1300",
	"invalid-http-status":                        "VF1301",
	"invalid-http-method":                        "VF1302",
	"invalid-url":                                "VF1303",
	"request-url-shape":                          "VF1304",
	"null-query-value":                           "VF1305",
	"null-cookie-value":                          "VF1306",
	"conflicting-body-modes":                     "VF1307",
	"invalid-body-file-mode":                     "VF1308",
	"invalid-body-file":                          "VF1309",
	"invalid-auth":                               "VF1310",
	"invalid-use":                                "VF1311",
	"missing-use":                                "VF1312",
	"invalid-use-kind":                           "VF1313",
	"unknown-input":                              "VF1320",
	"missing-required-input":                     "VF1321",
	"invalid-input-type":                         "VF1322",
	"invalid-input-default":                      "VF1323",
	"invalid-input-value":                        "VF1324",
	"unknown-request-output":                     "VF1330",
	"extract-source":                             "VF1331",
	"from-definition-without-request-definition": "VF1332",
	"invalid-extraction-scope":                   "VF1333",
	"invalid-selector":                           "VF1401",
	"assertion-path-required":                    "VF1402",
	"unknown-assertion-operator":                 "VF1403",
	"invalid-assertion-operand":                  "VF1404",
	"invalid-regex":                              "VF1405",
	"invalid-status-expectation":                 "VF1406",
	"unknown-performance-metric":                 "VF1407",
	"environment-variables":                      "VF1501",
	"invalid-environment-inheritance":            "VF1502",
	"project-config-error":                       "VF1601",
	"project-config-unknown-field":               "VF1602",
	"project-config-invalid-value":               "VF1603",
	"project-root-not-found":                     "VF1604",
	"project-discovery-error":                    "VF1605",
}

func DiagnosticCode(name string) string {
	if code, ok := diagnosticCodes[name]; ok {
		return code
	}
	return "VF1999"
}

func NewDiagnostic(name, message string, severity DiagnosticSeverity, location DocumentLocation) Diagnostic {
	return Diagnostic{Code: DiagnosticCode(name), Name: name, Message: message, Severity: severity, Location: location}
}

func DiagnosticMatches(d Diagnostic, nameOrCode string) bool {
	return d.Name == nameOrCode || d.Code == nameOrCode
}

// LoadError marks failures caused by user-supplied project/spec content. These
// are validation failures, not Veriflow internal failures.
type LoadError struct {
	Name    string
	Path    string
	Line    int
	Column  int
	Message string
	Cause   error
}

func (e *LoadError) Error() string {
	loc := e.Path
	if e.Line > 0 {
		loc += ":" + strconv.Itoa(e.Line)
		if e.Column > 0 {
			loc += ":" + strconv.Itoa(e.Column)
		}
	}
	if loc == "" {
		return e.Message
	}
	return loc + ": " + e.Message
}

func (e *LoadError) Unwrap() error { return e.Cause }

func loadErrorDiagnostic(err error, fallbackPath string) Diagnostic {
	var le *LoadError
	if errors.As(err, &le) {
		path := le.Path
		if path == "" {
			path = fallbackPath
		}
		name := le.Name
		if name == "" {
			name = "load-error"
		}
		return NewDiagnostic(name, le.Message, ErrorSeverity, DocumentLocation{File: path, DocumentPath: "$", Line: le.Line, Column: le.Column})
	}
	return NewDiagnostic("load-error", err.Error(), ErrorSeverity, DocumentLocation{File: fallbackPath, DocumentPath: "$"})
}

var yamlPositionRE = regexp.MustCompile(`(?i)line\s+(\d+)(?::(\d+))?`)

func classifyYAMLLoadError(path string, err error) *LoadError {
	name := "yaml-parse-error"
	msg := strings.TrimSpace(err.Error())
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "duplicate") && strings.Contains(lower, "key") {
		name = "yaml-duplicate-key"
	} else if strings.Contains(lower, "depth") || strings.Contains(lower, "nesting") {
		name = "yaml-depth-limit"
	} else if strings.Contains(lower, "alias") {
		name = "yaml-alias-limit"
	} else if strings.Contains(lower, "utf-8") || strings.Contains(lower, "utf8") {
		name = "invalid-utf8"
	}
	line, column := 0, 0
	if match := yamlPositionRE.FindStringSubmatch(msg); len(match) > 0 {
		line, _ = strconv.Atoi(match[1])
		if len(match) > 2 && match[2] != "" {
			column, _ = strconv.Atoi(match[2])
		}
	}
	return &LoadError{Name: name, Path: path, Line: line, Column: column, Message: msg, Cause: err}
}

func userLoadError(name, path, format string, args ...any) *LoadError {
	return &LoadError{Name: name, Path: path, Message: fmt.Sprintf(format, args...)}
}

func IsUserLoadError(err error) bool {
	var le *LoadError
	return errors.As(err, &le)
}

// DiagnosticFromError converts a user-caused load/parse failure into the same
// stable diagnostic representation used by semantic validation.
func DiagnosticFromError(err error, fallbackPath string) (Diagnostic, bool) {
	if !IsUserLoadError(err) {
		return Diagnostic{}, false
	}
	return loadErrorDiagnostic(err, fallbackPath), true
}

// Runtime diagnostic codes are a stable 1.0 machine-readable contract for
// failures that occur after a valid spec begins executing.
var runtimeDiagnosticCodes = map[string]string{
	"unresolved-variable":        "VF2001",
	"variable-cycle":             "VF2002",
	"variable-expansion-depth":   "VF2003",
	"input-contract-error":       "VF2004",
	"request-timeout":            "VF3001",
	"network-error":              "VF3002",
	"request-build-error":        "VF3003",
	"response-too-large":         "VF3004",
	"execution-cancelled":        "VF3005",
	"request-reference-error":    "VF3006",
	"filesystem-error":           "VF3007",
	"assertion-evaluation-error": "VF4001",
	"extraction-error":           "VF4002",
	"reporter-error":             "VF5001",
	"report-write-error":         "VF5002",
	"serialization-error":        "VF5003",
	"no-tests-selected":          "VF5004",
	"execution-error":            "VF3999",
}

type RuntimeFailureError struct {
	Name string
	Err  error
}

func (e *RuntimeFailureError) Error() string {
	if e == nil || e.Err == nil {
		return "runtime failure"
	}
	return e.Err.Error()
}

func (e *RuntimeFailureError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func runtimeNamedError(name string, err error) error {
	if err == nil {
		return nil
	}
	var named *RuntimeFailureError
	if errors.As(err, &named) {
		return err
	}
	var interpolation InterpolationError
	if errors.As(err, &interpolation) {
		return err
	}
	return &RuntimeFailureError{Name: name, Err: err}
}

func runtimeHTTPError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) {
		return runtimeNamedError("execution-cancelled", err)
	}
	if IsTimeoutError(err) || errors.Is(err, context.DeadlineExceeded) {
		return runtimeNamedError("request-timeout", err)
	}
	var tooLarge *ResponseTooLargeError
	if errors.As(err, &tooLarge) {
		return runtimeNamedError("response-too-large", err)
	}
	return runtimeNamedError("network-error", err)
}

func RuntimeDiagnosticCode(name string) string {
	if code, ok := runtimeDiagnosticCodes[name]; ok {
		return code
	}
	return "VF3999"
}

func NewRuntimeDiagnostic(name, message string) RuntimeDiagnostic {
	return RuntimeDiagnostic{Code: RuntimeDiagnosticCode(name), Name: name, Message: message}
}

func RuntimeDiagnosticFromError(err error) RuntimeDiagnostic {
	if err == nil {
		return RuntimeDiagnostic{}
	}
	var named *RuntimeFailureError
	if errors.As(err, &named) && named.Name != "" {
		return NewRuntimeDiagnostic(named.Name, named.Error())
	}
	var interpolation InterpolationError
	if errors.As(err, &interpolation) {
		name := interpolation.Name
		if name == "" {
			name = "unresolved-variable"
		}
		return NewRuntimeDiagnostic(name, interpolation.Error())
	}
	return NewRuntimeDiagnostic("execution-error", err.Error())
}

// DiagnosticRegistrySnapshot returns copies so tests and integrations can
// verify uniqueness without mutating the registry.
func DiagnosticRegistrySnapshot() map[string]string {
	out := make(map[string]string, len(diagnosticCodes)+len(runtimeDiagnosticCodes))
	for name, code := range diagnosticCodes {
		out[name] = code
	}
	for name, code := range runtimeDiagnosticCodes {
		out[name] = code
	}
	return out
}

func IsKnownDiagnosticName(name string) bool {
	if _, ok := diagnosticCodes[name]; ok {
		return true
	}
	_, ok := runtimeDiagnosticCodes[name]
	return ok
}
