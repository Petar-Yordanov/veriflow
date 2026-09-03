package engine

import (
	"path/filepath"
	"time"
)

const (
	EngineName                 = "veriflow"
	RedactionValue             = "*******"
	DefaultEncoding            = "utf-8"
	SpecFormatVersion          = "1.0"
	ReportSchemaVersion        = "1.0"
	EventSchemaVersion         = "1.0"
	ProjectConfigSchemaVersion = "1.0"
)

// These variables are intentionally writable so release builds can inject exact
// build metadata through -ldflags -X without generating source files.
var (
	EngineVersion = "1.0.0"
	BuildCommit   = "unknown"
	BuildDate     = "unknown"
)

type SpecKind string

const (
	RequestDefinitionKind SpecKind = "requestDefinition"
	TestSuiteKind         SpecKind = "testSuite"
	EnvironmentKind       SpecKind = "environment"
)

type ResultStatus string

const (
	Passed  ResultStatus = "passed"
	Failed  ResultStatus = "failed"
	Skipped ResultStatus = "skipped"
)

type VariableScope string

const (
	SuiteScope VariableScope = "suite"
	TestScope  VariableScope = "test"
	StepScope  VariableScope = "step"
)

type SourceRef struct {
	File         string `json:"file,omitempty"`
	DocumentPath string `json:"documentPath,omitempty"`
	Line         int    `json:"line,omitempty"`
	Column       int    `json:"column,omitempty"`
}

type SpecMetadata struct {
	FormatVersion string     `json:"formatVersion"`
	Kind          SpecKind   `json:"kind"`
	ID            string     `json:"id,omitempty"`
	Name          string     `json:"name,omitempty"`
	Description   string     `json:"description,omitempty"`
	Source        *SourceRef `json:"source,omitempty"`
}

type TimingMetrics struct {
	TotalMS *float64 `json:"totalMs,omitempty"`
	AvgMS   *float64 `json:"avgMs,omitempty"`
	P95MS   *float64 `json:"p95Ms,omitempty"`
	MaxMS   *float64 `json:"maxMs,omitempty"`
}

type RetrySummary struct {
	Attempts int      `json:"attempts"`
	Retried  bool     `json:"retried"`
	Reasons  []string `json:"reasons,omitempty"`
}

type AssertionClause struct {
	Path         string            `json:"path,omitempty"`
	ElementField string            `json:"field,omitempty"`
	Operators    map[string]any    `json:"operators,omitempty"`
	And          []AssertionClause `json:"and,omitempty"`
	Or           []AssertionClause `json:"or,omitempty"`
	Source       *SourceRef        `json:"source,omitempty"`
}

type HeaderExpectation struct{ Operators map[string]any }
type TextExpectation struct{ Operators map[string]any }
type BinaryExpectation struct{ Operators map[string]any }
type PerformanceExpectation struct{ Metrics map[string]map[string]any }

type ExpectationSpec struct {
	Status      any                          `json:"status,omitempty"`
	Body        *AssertionClause             `json:"body,omitempty"`
	Headers     map[string]HeaderExpectation `json:"headers,omitempty"`
	Text        *TextExpectation             `json:"text,omitempty"`
	Binary      *BinaryExpectation           `json:"binary,omitempty"`
	Performance *PerformanceExpectation      `json:"performance,omitempty"`
}

type InputDefinition struct {
	Type        string     `json:"type,omitempty"`
	Required    bool       `json:"required,omitempty"`
	Sensitive   bool       `json:"sensitive,omitempty"`
	Description string     `json:"description,omitempty"`
	Default     any        `json:"default,omitempty"`
	Source      *SourceRef `json:"source,omitempty"`
}

type OutputDefinition struct {
	Path      string     `json:"path"`
	Required  bool       `json:"required,omitempty"`
	Sensitive bool       `json:"sensitive,omitempty"`
	Source    *SourceRef `json:"source,omitempty"`
}

type PathParamEncoding struct {
	Enabled bool `json:"enabled"`
}

type AuthSpec struct {
	Type     string `json:"type,omitempty"`
	Username string `json:"username,omitempty"`
	Password string `json:"password,omitempty"`
	Token    string `json:"token,omitempty"`
	Name     string `json:"name,omitempty"`
	Value    string `json:"value,omitempty"`
	In       string `json:"in,omitempty"`
}

type RequestSpec struct {
	Method            string            `json:"method"`
	URL               string            `json:"url,omitempty"`
	BaseURL           string            `json:"baseUrl,omitempty"`
	Path              string            `json:"path,omitempty"`
	PathParams        map[string]any    `json:"pathParams,omitempty"`
	PathParamEncoding PathParamEncoding `json:"pathParamEncoding,omitempty"`
	Query             map[string]any    `json:"query,omitempty"`
	Headers           map[string]any    `json:"headers,omitempty"`
	Cookies           map[string]any    `json:"cookies,omitempty"`
	Auth              *AuthSpec         `json:"auth,omitempty"`
	Body              any               `json:"body,omitempty"`
	BodyRaw           string            `json:"bodyRaw,omitempty"`
	BodyFile          string            `json:"bodyFile,omitempty"`
	BodyFileMode      string            `json:"bodyFileMode,omitempty"`
	Form              map[string]any    `json:"form,omitempty"`
	Multipart         map[string]any    `json:"multipart,omitempty"`
	TimeoutMS         int               `json:"timeoutMs,omitempty"`
	FollowRedirects   *bool             `json:"followRedirects,omitempty"`
	Source            *SourceRef        `json:"source,omitempty"`
}

type RequestDefinitionSpec struct {
	Metadata SpecMetadata                `json:"metadata"`
	Request  RequestSpec                 `json:"request"`
	Inputs   map[string]InputDefinition  `json:"inputs,omitempty"`
	Outputs  map[string]OutputDefinition `json:"outputs,omitempty"`
	Raw      map[string]any              `json:"raw,omitempty"`
	Path     string                      `json:"-"`
}

type EnvironmentSpec struct {
	Metadata  SpecMetadata   `json:"metadata"`
	Name      string         `json:"name,omitempty"`
	Extends   string         `json:"extends,omitempty"`
	Variables map[string]any `json:"variables,omitempty"`
	Raw       map[string]any `json:"raw,omitempty"`
}

type WaitSpec struct {
	BeforeMS int `json:"beforeMs,omitempty"`
	AfterMS  int `json:"afterMs,omitempty"`
	ForMS    int `json:"forMs,omitempty"`
}

type RetryCondition struct {
	StatusIn      []int `json:"statusIn,omitempty"`
	NetworkErrors bool  `json:"networkErrors,omitempty"`
	Timeouts      bool  `json:"timeouts,omitempty"`
}
type RetrySpec struct {
	Count             int            `json:"count,omitempty"`
	DelayMS           int            `json:"delayMs,omitempty"`
	BackoffMultiplier float64        `json:"backoffMultiplier,omitempty"`
	MaxDelayMS        int            `json:"maxDelayMs,omitempty"`
	When              RetryCondition `json:"when,omitempty"`
}
type RepeatSpec struct {
	WarmupCount int `json:"warmupCount,omitempty"`
	Count       int `json:"count,omitempty"`
}
type LogSideSpec struct {
	Headers *bool `json:"headers,omitempty"`
	Body    *bool `json:"body,omitempty"`
}
type LogSpec struct {
	Request  LogSideSpec `json:"request,omitempty"`
	Response LogSideSpec `json:"response,omitempty"`
}
type ArtifactSpec struct {
	SaveResponseBodyTo string `json:"saveResponseBodyTo,omitempty"`
	SaveParsedJSONTo   string `json:"saveParsedJsonTo,omitempty"`
	SaveHeadersTo      string `json:"saveHeadersTo,omitempty"`
	SaveTimingTo       string `json:"saveTimingTo,omitempty"`
}
type ExtractionSpec struct {
	FromSelector   string        `json:"from,omitempty"`
	FromDefinition string        `json:"fromDefinition,omitempty"`
	FromHeader     string        `json:"fromHeader,omitempty"`
	FromCookie     string        `json:"fromCookie,omitempty"`
	FromTextRegex  string        `json:"fromTextRegex,omitempty"`
	FromStatus     bool          `json:"fromStatus,omitempty"`
	Scope          VariableScope `json:"scope,omitempty"`
	Required       bool          `json:"required,omitempty"`
	Sensitive      bool          `json:"sensitive,omitempty"`
}
type StepSpec struct {
	ID                string                    `json:"id,omitempty"`
	Name              string                    `json:"name,omitempty"`
	Skip              bool                      `json:"skip,omitempty"`
	ContinueOnFailure bool                      `json:"continueOnFailure,omitempty"`
	Variables         map[string]any            `json:"variables,omitempty"`
	Wait              *WaitSpec                 `json:"wait,omitempty"`
	Use               string                    `json:"use,omitempty"`
	With              map[string]any            `json:"with,omitempty"`
	Request           *RequestSpec              `json:"request,omitempty"`
	Extend            map[string]any            `json:"extend,omitempty"`
	Overrides         map[string]any            `json:"overrides,omitempty"`
	TimeoutMS         int                       `json:"timeoutMs,omitempty"`
	Expect            *ExpectationSpec          `json:"expect,omitempty"`
	Extract           map[string]ExtractionSpec `json:"extract,omitempty"`
	Retry             *RetrySpec                `json:"retry,omitempty"`
	Repeat            *RepeatSpec               `json:"repeat,omitempty"`
	Log               *LogSpec                  `json:"log,omitempty"`
	Artifacts         *ArtifactSpec             `json:"artifacts,omitempty"`
	Source            *SourceRef                `json:"source,omitempty"`
}
type TestCaseSpec struct {
	Name      string         `json:"name,omitempty"`
	Variables map[string]any `json:"variables,omitempty"`
	Skip      bool           `json:"skip,omitempty"`
	Source    *SourceRef     `json:"source,omitempty"`
}

type TestSpec struct {
	ID         string                  `json:"id"`
	BaseID     string                  `json:"-"`
	CaseID     string                  `json:"-"`
	Name       string                  `json:"name,omitempty"`
	Tags       []string                `json:"tags,omitempty"`
	Skip       bool                    `json:"skip,omitempty"`
	SkipReason string                  `json:"skipReason,omitempty"`
	TimeoutMS  int                     `json:"timeoutMs,omitempty"`
	Variables  map[string]any          `json:"variables,omitempty"`
	Cases      map[string]TestCaseSpec `json:"cases,omitempty"`
	Steps      []StepSpec              `json:"steps,omitempty"`
	Source     *SourceRef              `json:"source,omitempty"`
}

type SuiteHooks struct {
	BeforeAll  []StepSpec `json:"beforeAll,omitempty"`
	AfterAll   []StepSpec `json:"afterAll,omitempty"`
	BeforeEach []StepSpec `json:"beforeEach,omitempty"`
	AfterEach  []StepSpec `json:"afterEach,omitempty"`
}
type SuiteDefaults struct {
	TimeoutMS       int            `json:"timeoutMs,omitempty"`
	FollowRedirects *bool          `json:"followRedirects,omitempty"`
	Headers         map[string]any `json:"headers,omitempty"`
	Retry           *RetrySpec     `json:"retry,omitempty"`
}
type SuiteInfo struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
}
type GlobalsSpec struct {
	Variables map[string]any `json:"variables,omitempty"`
}
type TestSuiteSpec struct {
	Metadata  SpecMetadata   `json:"metadata"`
	Info      SuiteInfo      `json:"info,omitempty"`
	TimeoutMS int            `json:"timeoutMs,omitempty"`
	Globals   GlobalsSpec    `json:"globals,omitempty"`
	Defaults  SuiteDefaults  `json:"defaults,omitempty"`
	Hooks     SuiteHooks     `json:"hooks,omitempty"`
	Tests     []TestSpec     `json:"tests,omitempty"`
	Raw       map[string]any `json:"raw,omitempty"`
}

type LoadedDocument struct {
	Path      string            `json:"path"`
	Raw       map[string]any    `json:"raw"`
	Typed     any               `json:"-"`
	SourceMap map[string][2]int `json:"-"`
}
type LoadedBundle struct {
	Suite                LoadedDocument
	Environment          *LoadedDocument
	ReferencedRequests   map[string]LoadedDocument
	ReferenceDiagnostics []Diagnostic
	ProjectRoot          string
}

type DiagnosticSeverity string

const (
	ErrorSeverity   DiagnosticSeverity = "error"
	WarningSeverity DiagnosticSeverity = "warning"
	InfoSeverity    DiagnosticSeverity = "info"
)

type DocumentLocation struct {
	File         string `json:"file,omitempty"`
	DocumentPath string `json:"path,omitempty"`
	Line         int    `json:"line,omitempty"`
	Column       int    `json:"column,omitempty"`
}
type Diagnostic struct {
	Code     string             `json:"code"`
	Name     string             `json:"name,omitempty"`
	Message  string             `json:"message"`
	Severity DiagnosticSeverity `json:"severity"`
	Location DocumentLocation   `json:"location"`
	Details  map[string]any     `json:"details,omitempty"`
}

func (d Diagnostic) IsError() bool { return d.Severity == ErrorSeverity }

type ValidationResult struct {
	Diagnostics []Diagnostic `json:"diagnostics"`
}

func (v ValidationResult) OK() bool {
	for _, d := range v.Diagnostics {
		if d.IsError() {
			return false
		}
	}
	return true
}

type EngineEvent struct {
	SchemaVersion string         `json:"schemaVersion"`
	EngineVersion string         `json:"engineVersion"`
	EventType     string         `json:"event_type"`
	Timestamp     string         `json:"timestamp"`
	Payload       map[string]any `json:"payload"`
}

func NewEvent(kind string, payload map[string]any) EngineEvent {
	return EngineEvent{SchemaVersion: EventSchemaVersion, EngineVersion: EngineVersion, EventType: kind, Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Payload: payload}
}

type AssertionEvaluation struct {
	Target   string `json:"target"`
	Operator string `json:"operator"`
	Expected any    `json:"expected"`
	Actual   any    `json:"actual"`
	Passed   bool   `json:"passed"`
	Message  string `json:"message,omitempty"`
}
type ExtractionResult struct {
	Name      string `json:"name"`
	Scope     string `json:"scope"`
	Value     any    `json:"value,omitempty"`
	Sensitive bool   `json:"sensitive,omitempty"`
	Missing   bool   `json:"missing,omitempty"`
	Passed    bool   `json:"passed"`
	Message   string `json:"message,omitempty"`
}
type RequestSummary struct {
	Method  string         `json:"method"`
	URL     string         `json:"url"`
	Headers map[string]any `json:"headers,omitempty"`
}
type ResponseSummary struct {
	StatusCode  int               `json:"statusCode,omitempty"`
	Headers     map[string]string `json:"headers,omitempty"`
	BodyPreview string            `json:"bodyPreview,omitempty"`
}
type RuntimeDiagnostic struct {
	Code    string `json:"code"`
	Name    string `json:"name"`
	Message string `json:"message"`
}
type StepResult struct {
	ID              string                `json:"id,omitempty"`
	Name            string                `json:"name,omitempty"`
	Status          ResultStatus          `json:"status"`
	StartedAt       string                `json:"startedAt"`
	FinishedAt      string                `json:"finishedAt"`
	DurationMS      float64               `json:"durationMs"`
	RequestSummary  *RequestSummary       `json:"requestSummary,omitempty"`
	ResponseSummary *ResponseSummary      `json:"responseSummary,omitempty"`
	Assertions      []AssertionEvaluation `json:"assertions,omitempty"`
	Extractions     []ExtractionResult    `json:"extractions,omitempty"`
	RetrySummary    RetrySummary          `json:"retrySummary"`
	Timing          TimingMetrics         `json:"timing"`
	Error           string                `json:"error,omitempty"`
	RuntimeError    *RuntimeDiagnostic    `json:"runtimeError,omitempty"`
	Artifacts       []string              `json:"artifacts,omitempty"`
}
type TestResult struct {
	ID           string             `json:"id"`
	BaseID       string             `json:"baseId,omitempty"`
	Case         string             `json:"case,omitempty"`
	Name         string             `json:"name,omitempty"`
	Status       ResultStatus       `json:"status"`
	StartedAt    string             `json:"startedAt"`
	FinishedAt   string             `json:"finishedAt"`
	DurationMS   float64            `json:"durationMs"`
	Error        string             `json:"error,omitempty"`
	RuntimeError *RuntimeDiagnostic `json:"runtimeError,omitempty"`
	Tags         []string           `json:"tags,omitempty"`
	BeforeEach   []StepResult       `json:"beforeEach,omitempty"`
	Steps        []StepResult       `json:"steps,omitempty"`
	AfterEach    []StepResult       `json:"afterEach,omitempty"`
}
type SuiteResult struct {
	SchemaVersion    string             `json:"schemaVersion"`
	EngineVersion    string             `json:"engineVersion"`
	Name             string             `json:"name,omitempty"`
	Status           ResultStatus       `json:"status"`
	StatusCode       int                `json:"statusCode"`
	ValidationFailed bool               `json:"validationFailed,omitempty"`
	StartedAt        string             `json:"startedAt"`
	FinishedAt       string             `json:"finishedAt"`
	DurationMS       float64            `json:"durationMs"`
	Error            string             `json:"error,omitempty"`
	RuntimeError     *RuntimeDiagnostic `json:"runtimeError,omitempty"`
	PassedCount      int                `json:"passedCount"`
	FailedCount      int                `json:"failedCount"`
	SkippedCount     int                `json:"skippedCount"`
	BeforeAll        []StepResult       `json:"beforeAll,omitempty"`
	Tests            []TestResult       `json:"tests,omitempty"`
	AfterAll         []StepResult       `json:"afterAll,omitempty"`
	Diagnostics      []map[string]any   `json:"diagnostics,omitempty"`
}
type ConsolidatedResult struct {
	SchemaVersion          string       `json:"schemaVersion"`
	EngineVersion          string       `json:"engineVersion"`
	Suites                 int          `json:"suites"`
	TotalTests             int          `json:"totalTests"`
	Passed                 int          `json:"passed"`
	Failed                 int          `json:"failed"`
	Skipped                int          `json:"skipped"`
	ValidationFailedSuites int          `json:"validationFailedSuites,omitempty"`
	DurationMS             float64      `json:"durationMs"`
	OverallStatus          ResultStatus `json:"status"`
	StatusCode             int          `json:"statusCode"`
}

func ValidationFailureResult(name string, diagnostics []Diagnostic) SuiteResult {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	return SuiteResult{
		SchemaVersion: ReportSchemaVersion, EngineVersion: EngineVersion, Name: name,
		Status: Failed, StatusCode: -1, ValidationFailed: true, StartedAt: now, FinishedAt: now,
		Diagnostics: diagMaps(diagnostics),
	}
}

func Consolidate(results []SuiteResult) ConsolidatedResult {
	out := ConsolidatedResult{SchemaVersion: ReportSchemaVersion, EngineVersion: EngineVersion, Suites: len(results), OverallStatus: Passed, StatusCode: 0}
	for _, r := range results {
		out.TotalTests += len(r.Tests)
		out.Passed += r.PassedCount
		out.Failed += r.FailedCount
		out.Skipped += r.SkippedCount
		if r.ValidationFailed {
			out.ValidationFailedSuites++
		}
		out.DurationMS += r.DurationMS
		if r.Status == Failed {
			out.OverallStatus = Failed
			out.StatusCode = -1
		}
	}
	if len(results) > 0 && out.Failed == 0 && out.Passed == 0 && out.Skipped > 0 {
		out.OverallStatus = Skipped
	}
	return out
}

func inferProjectRoot(suitePath string) (string, error) {
	abs, err := absPath(suitePath)
	if err != nil {
		return "", err
	}
	cur := filepath.Dir(abs)
	fallback := cur
	for {
		if dirExists(filepath.Join(cur, "suites")) || dirExists(filepath.Join(cur, "requests")) || dirExists(filepath.Join(cur, "environments")) {
			return cur, nil
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return fallback, nil
		}
		cur = parent
	}
}
