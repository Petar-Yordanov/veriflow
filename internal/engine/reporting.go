package engine

import (
	"bufio"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type Reporter interface {
	OnEvent(EngineEvent) error
	Finalize(SuiteResult) error
}

type SummaryReporter struct {
	Events      []EngineEvent
	FinalResult *SuiteResult
}

func (r *SummaryReporter) OnEvent(e EngineEvent) error       { r.Events = append(r.Events, e); return nil }
func (r *SummaryReporter) Finalize(result SuiteResult) error { r.FinalResult = &result; return nil }

type JSONFileReporter struct {
	OutputPath string
	Events     []EngineEvent
}

func (r *JSONFileReporter) OnEvent(e EngineEvent) error { r.Events = append(r.Events, e); return nil }
func (r *JSONFileReporter) Finalize(result SuiteResult) error {
	if err := os.MkdirAll(filepath.Dir(r.OutputPath), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(map[string]any{"events": r.Events, "result": result}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.OutputPath, b, 0644)
}

type LiveReporter struct {
	// JSONOutput is retained for library callers that explicitly want an event
	// stream on stdout. The stable CLI --json contract uses Quiet and prints one
	// final JSON document; event streaming belongs in --event-jsonl.
	JSONOutput     bool
	Quiet          bool
	EventJSONLPath string
	mu             sync.Mutex
}

func (r *LiveReporter) OnEvent(e EngineEvent) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.EventJSONLPath != "" {
		if err := appendJSONL(r.EventJSONLPath, e); err != nil {
			return err
		}
	}
	if r.Quiet {
		return nil
	}
	if r.JSONOutput {
		b, err := json.Marshal(e)
		if err != nil {
			return fmt.Errorf("serialize event: %w", err)
		}
		fmt.Println(string(b))
		return nil
	}
	text, err := renderEvent(e)
	if err != nil {
		return err
	}
	fmt.Println(text)
	return nil
}
func (r *LiveReporter) Finalize(SuiteResult) error { return nil }
func appendJSONL(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err = w.Write(append(b, '\n')); err != nil {
		return err
	}
	return w.Flush()
}
func renderEvent(e EngineEvent) (string, error) {
	p := e.Payload
	switch e.EventType {
	case "suite.started":
		return fmt.Sprintf("[SUITE START] %v", p["name"]), nil
	case "suite.finished":
		return fmt.Sprintf("[SUITE END] status=%v passed=%v failed=%v skipped=%v", p["status"], p["passedCount"], p["failedCount"], p["skippedCount"]), nil
	case "test.started":
		return fmt.Sprintf("  [TEST START] %v", p["id"]), nil
	case "test.finished":
		return fmt.Sprintf("  [TEST END] %v status=%v", p["id"], p["status"]), nil
	case "hook.started":
		if p["testId"] != nil && p["testId"] != "__beforeAll__" && p["testId"] != "__afterAll__" {
			return fmt.Sprintf("  [HOOK START] %v test=%v", p["hook"], p["testId"]), nil
		}
		return fmt.Sprintf("  [HOOK START] %v", p["hook"]), nil
	case "hook.finished":
		if p["testId"] != nil && p["testId"] != "__beforeAll__" && p["testId"] != "__afterAll__" {
			return fmt.Sprintf("  [HOOK END] %v test=%v passed=%v", p["hook"], p["testId"], p["passed"]), nil
		}
		return fmt.Sprintf("  [HOOK END] %v passed=%v", p["hook"], p["passed"]), nil
	case "step.started":
		return fmt.Sprintf("    [STEP START] %v", p["id"]), nil
	case "step.finished":
		return fmt.Sprintf("    [STEP END] %v status=%v", p["id"], p["status"]), nil
	case "request.prepared":
		return fmt.Sprintf("      [REQUEST] %v %v", p["method"], p["url"]), nil
	case "request.log":
		b, err := json.Marshal(p)
		if err != nil {
			return "", fmt.Errorf("serialize request log event: %w", err)
		}
		return fmt.Sprintf("      [REQUEST LOG] %s", string(b)), nil
	case "response.received":
		return fmt.Sprintf("      [RESPONSE] %v in %.2f ms", p["statusCode"], asFloatDefault(p["totalMs"])), nil
	case "response.log":
		b, err := json.Marshal(p)
		if err != nil {
			return "", fmt.Errorf("serialize response log event: %w", err)
		}
		return fmt.Sprintf("      [RESPONSE LOG] %s", string(b)), nil
	case "assertions.evaluated":
		return fmt.Sprintf("      [ASSERTIONS] passed=%v count=%v", p["passed"], p["count"]), nil
	case "extraction.completed":
		return fmt.Sprintf("      [EXTRACTION] passed=%v names=%v", p["passed"], p["names"]), nil
	case "artifact.saved":
		return fmt.Sprintf("      [ARTIFACT] saved: %v", p["path"]), nil
	case "validation.error":
		return fmt.Sprintf("  [VALIDATION ERROR] [%v] %v", p["code"], p["message"]), nil
	case "runtime.error":
		return fmt.Sprintf("  [RUNTIME ERROR] %v", p["message"]), nil
	}
	b, err := json.Marshal(e)
	if err != nil {
		return "", fmt.Errorf("serialize event: %w", err)
	}
	return string(b), nil
}
func asFloatDefault(v any) float64 { f, _ := asFloat(v); return f }

type MultiReporter []Reporter

func (m MultiReporter) OnEvent(e EngineEvent) error {
	for _, r := range m {
		if err := r.OnEvent(e); err != nil {
			return err
		}
	}
	return nil
}
func (m MultiReporter) Finalize(s SuiteResult) error {
	for _, r := range m {
		if err := r.Finalize(s); err != nil {
			return err
		}
	}
	return nil
}

type RunReport struct {
	SchemaVersion string             `json:"schemaVersion"`
	EngineVersion string             `json:"engineVersion"`
	Result        ConsolidatedResult `json:"result"`
	Suites        []SuiteResult      `json:"suites"`
	Failure       *RuntimeDiagnostic `json:"failure,omitempty"`
}

func BuildRunReport(suites []SuiteResult, failure *RuntimeDiagnostic) RunReport {
	result := Consolidate(suites)
	if failure != nil {
		result.OverallStatus = Failed
		result.StatusCode = -1
	}
	return RunReport{SchemaVersion: ReportSchemaVersion, EngineVersion: EngineVersion, Result: result, Suites: suites, Failure: failure}
}

func WriteRunReport(path string, suites []SuiteResult) error {
	return WriteRunReportValue(path, BuildRunReport(suites, nil))
}

func WriteRunReportValue(path string, report RunReport) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	b, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0644)
}

type junitSuites struct {
	XMLName  xml.Name     `xml:"testsuites"`
	Name     string       `xml:"name,attr,omitempty"`
	Tests    int          `xml:"tests,attr"`
	Failures int          `xml:"failures,attr"`
	Skipped  int          `xml:"skipped,attr"`
	Time     string       `xml:"time,attr"`
	Suites   []junitSuite `xml:"testsuite"`
}

type junitSuite struct {
	Name     string          `xml:"name,attr"`
	Tests    int             `xml:"tests,attr"`
	Failures int             `xml:"failures,attr"`
	Skipped  int             `xml:"skipped,attr"`
	Time     string          `xml:"time,attr"`
	Cases    []junitTestCase `xml:"testcase"`
}

type junitTestCase struct {
	Name      string        `xml:"name,attr"`
	Classname string        `xml:"classname,attr,omitempty"`
	Time      string        `xml:"time,attr"`
	Failure   *junitFailure `xml:"failure,omitempty"`
	Skipped   *struct{}     `xml:"skipped,omitempty"`
}

type junitFailure struct {
	Message string `xml:"message,attr,omitempty"`
	Type    string `xml:"type,attr,omitempty"`
	Body    string `xml:",chardata"`
}

func WriteJUnit(path string, suites []SuiteResult) error {
	return WriteJUnitWithFailure(path, suites, nil)
}

func WriteJUnitWithFailure(path string, suites []SuiteResult, invocationFailure *RuntimeDiagnostic) error {
	root := junitSuites{Name: "Veriflow"}
	for _, s := range suites {
		js := junitSuite{Name: s.Name, Tests: len(s.Tests), Failures: s.FailedCount, Skipped: s.SkippedCount, Time: secondsString(s.DurationMS)}
		root.Tests += len(s.Tests)
		root.Failures += s.FailedCount
		root.Skipped += s.SkippedCount
		root.Time = secondsString(parseSeconds(root.Time) + s.DurationMS/1000.0)

		if s.ValidationFailed && len(s.Diagnostics) > 0 {
			message, body := summarizeValidationFailure(s.Diagnostics)
			js.Tests++
			js.Failures++
			root.Tests++
			root.Failures++
			js.Cases = append(js.Cases, junitTestCase{
				Name: "validation", Classname: s.Name, Time: "0.000000",
				Failure: &junitFailure{Message: message, Type: "veriflow.validation", Body: body},
			})
		}

		sameAsInvocationFailure := invocationFailure != nil && s.RuntimeError != nil &&
			s.RuntimeError.Code == invocationFailure.Code &&
			s.RuntimeError.Name == invocationFailure.Name &&
			s.RuntimeError.Message == invocationFailure.Message
		if s.Error != "" && !sameAsInvocationFailure {
			js.Tests++
			js.Failures++
			root.Tests++
			root.Failures++
			js.Cases = append(js.Cases, junitTestCase{
				Name: "suite-runtime", Classname: s.Name, Time: "0.000000",
				Failure: &junitFailure{Message: s.Error, Type: "veriflow.suite", Body: s.Error},
			})
		}

		if hook, ok := firstFailedStep(s.BeforeAll); ok {
			message, body := summarizeStepFailure("beforeAll", hook)
			js.Tests++
			js.Failures++
			root.Tests++
			root.Failures++
			js.Cases = append(js.Cases, junitTestCase{
				Name: "beforeAll", Classname: s.Name, Time: secondsString(hook.DurationMS),
				Failure: &junitFailure{Message: message, Type: "veriflow.hook", Body: body},
			})
		}

		for _, t := range s.Tests {
			name := t.ID
			if t.Name != "" {
				name = t.Name
			}
			jc := junitTestCase{Name: name, Classname: s.Name, Time: secondsString(t.DurationMS)}
			switch t.Status {
			case Skipped:
				jc.Skipped = &struct{}{}
			case Failed:
				message, body := summarizeTestFailure(t)
				jc.Failure = &junitFailure{Message: message, Type: "veriflow", Body: body}
			}
			js.Cases = append(js.Cases, jc)
		}

		if hook, ok := firstFailedStep(s.AfterAll); ok {
			message, body := summarizeStepFailure("afterAll", hook)
			js.Tests++
			js.Failures++
			root.Tests++
			root.Failures++
			js.Cases = append(js.Cases, junitTestCase{
				Name: "afterAll", Classname: s.Name, Time: secondsString(hook.DurationMS),
				Failure: &junitFailure{Message: message, Type: "veriflow.hook", Body: body},
			})
		}

		root.Suites = append(root.Suites, js)
	}
	if invocationFailure != nil {
		js := junitSuite{Name: "Veriflow invocation", Tests: 1, Failures: 1, Time: "0.000000"}
		js.Cases = append(js.Cases, junitTestCase{Name: "invocation", Classname: "Veriflow", Time: "0.000000", Failure: &junitFailure{Message: invocationFailure.Message, Type: invocationFailure.Code + "." + invocationFailure.Name, Body: invocationFailure.Message}})
		root.Tests++
		root.Failures++
		root.Suites = append(root.Suites, js)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	b, err := xml.MarshalIndent(root, "", "  ")
	if err != nil {
		return err
	}
	b = append([]byte(xml.Header), b...)
	b = append(b, '\n')
	return os.WriteFile(path, b, 0644)
}

func summarizeValidationFailure(diagnostics []map[string]any) (string, string) {
	if len(diagnostics) == 0 {
		return "Veriflow validation failed", "Veriflow validation failed"
	}
	lines := make([]string, 0, len(diagnostics))
	for _, d := range diagnostics {
		code := fmt.Sprint(d["code"])
		name := fmt.Sprint(d["name"])
		message := fmt.Sprint(d["message"])
		location := ""
		if loc, ok := d["location"].(map[string]any); ok {
			location = fmt.Sprint(loc["file"])
			if line := asInt(loc["line"]); line > 0 {
				location += fmt.Sprintf(":%d", line)
			}
		}
		label := strings.TrimSpace(strings.Join([]string{code, name}, " "))
		line := strings.TrimSpace(strings.Join([]string{location, label, message}, " "))
		lines = append(lines, line)
	}
	first := lines[0]
	if first == "" {
		first = "Veriflow validation failed"
	}
	return first, strings.Join(lines, "\n")
}

func secondsString(ms float64) string { return fmt.Sprintf("%.6f", ms/1000.0) }
func parseSeconds(s string) float64   { var f float64; _, _ = fmt.Sscanf(s, "%f", &f); return f }

func summarizeTestFailure(t TestResult) (string, string) {
	parts := []string{}
	message := "test failed"
	if t.Error != "" {
		message = t.Error
		parts = append(parts, t.Error)
	}
	for _, group := range []struct {
		phase string
		steps []StepResult
	}{
		{phase: "beforeEach", steps: t.BeforeEach},
		{phase: "step", steps: t.Steps},
		{phase: "afterEach", steps: t.AfterEach},
	} {
		for _, step := range group.steps {
			if step.Status != Failed {
				continue
			}
			stepMessage, body := summarizeStepFailure(group.phase, step)
			if message == "test failed" && stepMessage != "" {
				message = stepMessage
			}
			parts = append(parts, body)
		}
	}
	if len(parts) == 0 {
		parts = append(parts, message)
	}
	return message, strings.Join(parts, "\n")
}

func summarizeStepFailure(phase string, step StepResult) (string, string) {
	prefix := phase
	if prefix == "step" {
		prefix = "step"
	}
	label := strings.TrimSpace(prefix + " " + step.ID)
	message := "step failed"
	parts := []string{}
	if step.Error != "" {
		message = step.Error
		parts = append(parts, fmt.Sprintf("%s: %s", label, step.Error))
	}
	for _, a := range step.Assertions {
		if a.Passed {
			continue
		}
		line := fmt.Sprintf("%s assertion %s.%s expected=%v actual=%v", label, a.Target, a.Operator, a.Expected, a.Actual)
		if message == "step failed" && a.Message != "" {
			message = a.Message
		}
		parts = append(parts, line)
	}
	for _, x := range step.Extractions {
		if !x.Passed {
			if message == "step failed" && x.Message != "" {
				message = x.Message
			}
			parts = append(parts, fmt.Sprintf("%s extraction %s: %s", label, x.Name, x.Message))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%s: %s", label, message))
	}
	return message, strings.Join(parts, "\n")
}

func firstFailedStep(steps []StepResult) (StepResult, bool) {
	for _, step := range steps {
		if step.Status == Failed {
			return step, true
		}
	}
	return StepResult{}, false
}
