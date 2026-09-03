package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const ProjectConfigFile = "veriflow.yml"

type ProjectRuntimeConfig struct {
	RunTimeoutMS     int    `json:"runTimeoutMs,omitempty"`
	SuiteTimeoutMS   int    `json:"suiteTimeoutMs,omitempty"`
	TestTimeoutMS    int    `json:"testTimeoutMs,omitempty"`
	CleanupTimeoutMS int    `json:"cleanupTimeoutMs,omitempty"`
	MaxResponseBytes int64  `json:"maxResponseBytes,omitempty"`
	ArtifactsRoot    string `json:"artifactsRoot,omitempty"`
}

type ProjectReportsConfig struct {
	JSON             string `json:"json,omitempty"`
	JUnit            string `json:"junit,omitempty"`
	ConsolidatedJSON string `json:"consolidatedJson,omitempty"`
	ReportDir        string `json:"reportDir,omitempty"`
	EventJSONL       string `json:"eventJsonl,omitempty"`
}

type ProjectCIConfig struct {
	FailIfNoTests bool `json:"failIfNoTests,omitempty"`
}

type ProjectNetworkConfig struct {
	CAFile                  string `json:"caFile,omitempty"`
	ClientCert              string `json:"clientCert,omitempty"`
	ClientKey               string `json:"clientKey,omitempty"`
	Proxy                   string `json:"proxy,omitempty"`
	CookieJar               bool   `json:"cookieJar,omitempty"`
	InsecureSkipTLSVerify   bool   `json:"insecureSkipTlsVerify,omitempty"`
	ConnectTimeoutMS        int    `json:"connectTimeoutMs,omitempty"`
	TLSHandshakeTimeoutMS   int    `json:"tlsHandshakeTimeoutMs,omitempty"`
	ResponseHeaderTimeoutMS int    `json:"responseHeaderTimeoutMs,omitempty"`
}

type ProjectConfig struct {
	Runtime ProjectRuntimeConfig `json:"runtime,omitempty"`
	Reports ProjectReportsConfig `json:"reports,omitempty"`
	CI      ProjectCIConfig      `json:"ci,omitempty"`
	Network ProjectNetworkConfig `json:"network,omitempty"`
}

// LoadProjectConfig loads PROJECT_ROOT/veriflow.yml when present. It returns
// structured diagnostics for user-caused configuration problems and never
// silently guesses invalid values.
func LoadProjectConfig(projectRoot string) (ProjectConfig, []Diagnostic) {
	root, err := filepath.Abs(projectRoot)
	if err != nil {
		return ProjectConfig{}, []Diagnostic{NewDiagnostic("project-config-error", err.Error(), ErrorSeverity, DocumentLocation{File: filepath.Join(projectRoot, ProjectConfigFile), DocumentPath: "$"})}
	}
	path := filepath.Join(root, ProjectConfigFile)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return ProjectConfig{}, nil
	}
	if err != nil {
		return ProjectConfig{}, []Diagnostic{NewDiagnostic("project-config-error", fmt.Sprintf("cannot read project config: %v", err), ErrorSeverity, DocumentLocation{File: path, DocumentPath: "$"})}
	}
	if int64(len(data)) > DefaultMaxSpecBytes {
		return ProjectConfig{}, []Diagnostic{NewDiagnostic("project-config-invalid-value", fmt.Sprintf("project config exceeds maximum size of %d bytes", DefaultMaxSpecBytes), ErrorSeverity, DocumentLocation{File: path, DocumentPath: "$"})}
	}
	raw, smap, err := parseYAML(data)
	if err != nil {
		le := classifyYAMLLoadError(path, err)
		return ProjectConfig{}, []Diagnostic{NewDiagnostic(le.Name, le.Message, ErrorSeverity, DocumentLocation{File: path, DocumentPath: "$", Line: le.Line, Column: le.Column})}
	}
	loc := func(p string) DocumentLocation {
		l := DocumentLocation{File: path, DocumentPath: p}
		if pos, ok := smap[p]; ok {
			l.Line, l.Column = pos[0], pos[1]
		}
		return l
	}
	diagnostics := []Diagnostic{}
	unknown := func(m map[string]any, allowed map[string]bool, prefix string) {
		keys := make([]string, 0)
		for k := range m {
			if !allowed[k] {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			p := prefix + "." + k
			diagnostics = append(diagnostics, NewDiagnostic("project-config-unknown-field", fmt.Sprintf("unknown project config field %q", k), ErrorSeverity, loc(p)))
		}
	}
	unknown(raw, setOf("runtime", "reports", "ci", "network"), "$")
	for _, section := range []string{"runtime", "reports", "ci", "network"} {
		if v, ok := raw[section]; ok && v != nil {
			if _, ok := v.(map[string]any); !ok {
				diagnostics = append(diagnostics, NewDiagnostic("project-config-invalid-value", section+" must be a mapping", ErrorSeverity, loc("$."+section)))
			}
		}
	}

	cfg := ProjectConfig{}
	if m := asMap(raw["runtime"]); m != nil {
		unknown(m, setOf("runTimeoutMs", "suiteTimeoutMs", "testTimeoutMs", "cleanupTimeoutMs", "maxResponseBytes", "artifactsRoot"), "$.runtime")
		checkInt := func(key string) int {
			v, exists := m[key]
			if !exists || v == nil {
				return 0
			}
			if !isIntegerValue(v) || asInt(v) <= 0 {
				diagnostics = append(diagnostics, NewDiagnostic("project-config-invalid-value", key+" must be a positive integer", ErrorSeverity, loc("$.runtime."+key)))
				return 0
			}
			return asInt(v)
		}
		cfg.Runtime.RunTimeoutMS = checkInt("runTimeoutMs")
		cfg.Runtime.SuiteTimeoutMS = checkInt("suiteTimeoutMs")
		cfg.Runtime.TestTimeoutMS = checkInt("testTimeoutMs")
		cfg.Runtime.CleanupTimeoutMS = checkInt("cleanupTimeoutMs")
		if v, exists := m["maxResponseBytes"]; exists && v != nil {
			if !isIntegerValue(v) || asInt(v) <= 0 {
				diagnostics = append(diagnostics, NewDiagnostic("project-config-invalid-value", "maxResponseBytes must be a positive integer", ErrorSeverity, loc("$.runtime.maxResponseBytes")))
			} else {
				cfg.Runtime.MaxResponseBytes = int64(asInt(v))
			}
		}
		if v, ok := m["artifactsRoot"]; ok && v != nil {
			if _, ok := v.(string); !ok {
				diagnostics = append(diagnostics, NewDiagnostic("project-config-invalid-value", "artifactsRoot must be a string", ErrorSeverity, loc("$.runtime.artifactsRoot")))
			}
		}
		cfg.Runtime.ArtifactsRoot = asString(m["artifactsRoot"])
	}
	if m := asMap(raw["reports"]); m != nil {
		unknown(m, setOf("json", "junit", "consolidatedJson", "reportDir", "eventJsonl"), "$.reports")
		for _, key := range []string{"json", "junit", "consolidatedJson", "reportDir", "eventJsonl"} {
			if v, ok := m[key]; ok && v != nil {
				if _, ok := v.(string); !ok {
					diagnostics = append(diagnostics, NewDiagnostic("project-config-invalid-value", key+" must be a string", ErrorSeverity, loc("$.reports."+key)))
				}
			}
		}
		cfg.Reports = ProjectReportsConfig{JSON: asString(m["json"]), JUnit: asString(m["junit"]), ConsolidatedJSON: asString(m["consolidatedJson"]), ReportDir: asString(m["reportDir"]), EventJSONL: asString(m["eventJsonl"])}
	}
	if m := asMap(raw["ci"]); m != nil {
		unknown(m, setOf("failIfNoTests"), "$.ci")
		if v, ok := m["failIfNoTests"]; ok && v != nil {
			if _, ok := v.(bool); !ok {
				diagnostics = append(diagnostics, NewDiagnostic("project-config-invalid-value", "failIfNoTests must be a boolean", ErrorSeverity, loc("$.ci.failIfNoTests")))
			}
		}
		cfg.CI.FailIfNoTests = asBool(m["failIfNoTests"])
	}
	if m := asMap(raw["network"]); m != nil {
		unknown(m, setOf("caFile", "clientCert", "clientKey", "proxy", "cookieJar", "insecureSkipTlsVerify", "connectTimeoutMs", "tlsHandshakeTimeoutMs", "responseHeaderTimeoutMs"), "$.network")
		for _, key := range []string{"caFile", "clientCert", "clientKey", "proxy"} {
			if v, ok := m[key]; ok && v != nil {
				if _, ok := v.(string); !ok {
					diagnostics = append(diagnostics, NewDiagnostic("project-config-invalid-value", key+" must be a string", ErrorSeverity, loc("$.network."+key)))
				}
			}
		}
		for _, key := range []string{"cookieJar", "insecureSkipTlsVerify"} {
			if v, ok := m[key]; ok && v != nil {
				if _, ok := v.(bool); !ok {
					diagnostics = append(diagnostics, NewDiagnostic("project-config-invalid-value", key+" must be a boolean", ErrorSeverity, loc("$.network."+key)))
				}
			}
		}
		checkNetworkTimeout := func(key string) int {
			v, exists := m[key]
			if !exists || v == nil {
				return 0
			}
			if !isIntegerValue(v) || asInt(v) < 0 {
				diagnostics = append(diagnostics, NewDiagnostic("project-config-invalid-value", key+" must be a non-negative integer (0 disables the phase timeout)", ErrorSeverity, loc("$.network."+key)))
				return 0
			}
			return asInt(v)
		}
		cfg.Network = ProjectNetworkConfig{CAFile: asString(m["caFile"]), ClientCert: asString(m["clientCert"]), ClientKey: asString(m["clientKey"]), Proxy: asString(m["proxy"]), CookieJar: asBool(m["cookieJar"]), InsecureSkipTLSVerify: asBool(m["insecureSkipTlsVerify"]), ConnectTimeoutMS: checkNetworkTimeout("connectTimeoutMs"), TLSHandshakeTimeoutMS: checkNetworkTimeout("tlsHandshakeTimeoutMs"), ResponseHeaderTimeoutMS: checkNetworkTimeout("responseHeaderTimeoutMs")}
	}
	sortDiagnostics(diagnostics)
	return cfg, diagnostics
}

func ResolveProjectConfigPath(root, value string) string {
	if value == "" || filepath.IsAbs(value) {
		return value
	}
	return filepath.Join(root, value)
}
