package engine

import "encoding/json"

// SpecJSONSchema returns the editor/CI schema for the public 1.0 YAML format.
// The runtime validator remains authoritative; this schema is intentionally
// generated from source so `veriflow schema` can be pinned alongside the CLI.
func SpecJSONSchema() map[string]any {
	jsonValue := map[string]any{}
	stringMap := map[string]any{"type": "object", "additionalProperties": jsonValue}
	boolSchema := map[string]any{"type": "boolean"}
	nonNegativeInt := map[string]any{"type": "integer", "minimum": 0}

	retry := objectSchema(map[string]any{
		"count":             nonNegativeInt,
		"delayMs":           nonNegativeInt,
		"backoffMultiplier": map[string]any{"type": "number", "minimum": 0},
		"maxDelayMs":        nonNegativeInt,
		"when": objectSchema(map[string]any{
			"statusIn":      map[string]any{"type": "array", "items": map[string]any{"type": "integer", "minimum": 100, "maximum": 599}},
			"networkErrors": boolSchema,
			"timeouts":      boolSchema,
		}, nil),
	}, nil)

	auth := objectSchema(map[string]any{
		"type":     map[string]any{"type": "string", "enum": []string{"basic", "bearer", "apiKey", "api-key"}},
		"username": map[string]any{"type": "string"},
		"password": map[string]any{"type": "string"},
		"token":    map[string]any{"type": "string"},
		"name":     map[string]any{"type": "string"},
		"value":    map[string]any{"type": "string"},
		"in":       map[string]any{"type": "string", "enum": []string{"header", "query"}},
	}, []string{"type"})

	multipartPart := map[string]any{
		"oneOf": []any{
			jsonValue,
			objectSchema(map[string]any{
				"file":        map[string]any{"type": "string"},
				"filename":    map[string]any{"type": "string"},
				"contentType": map[string]any{"type": "string"},
			}, []string{"file"}),
		},
	}

	request := objectSchema(map[string]any{
		"method":            map[string]any{"type": "string"},
		"url":               map[string]any{"type": "string"},
		"baseUrl":           map[string]any{"type": "string"},
		"path":              map[string]any{"type": "string"},
		"pathParams":        stringMap,
		"pathParamEncoding": objectSchema(map[string]any{"enabled": boolSchema}, nil),
		"query":             stringMap,
		"headers":           stringMap,
		"cookies":           stringMap,
		"auth":              auth,
		"body":              jsonValue,
		"bodyRaw":           map[string]any{"type": "string"},
		"bodyFile":          map[string]any{"type": "string"},
		"bodyFileMode":      map[string]any{"type": "string", "enum": []string{"binary", "text", "json"}},
		"form":              stringMap,
		"multipart":         map[string]any{"type": "object", "additionalProperties": multipartPart},
		"timeoutMs":         nonNegativeInt,
		"followRedirects":   boolSchema,
	}, []string{"method"})

	inputDefinition := objectSchema(map[string]any{
		"type":        map[string]any{"type": "string", "enum": []string{"any", "string", "number", "integer", "int", "boolean", "bool", "object", "map", "array", "list", "null"}},
		"required":    boolSchema,
		"sensitive":   boolSchema,
		"description": map[string]any{"type": "string"},
		"default":     jsonValue,
	}, nil)
	outputDefinition := objectSchema(map[string]any{
		"path":      map[string]any{"type": "string"},
		"required":  boolSchema,
		"sensitive": boolSchema,
	}, []string{"path"})

	operatorProps := map[string]any{}
	for _, name := range []string{"exists", "isNull", "type", "equals", "notEquals", "equalsIgnoreCase", "in", "notIn", "matches", "contains", "notContains", "startsWith", "endsWith", "count", "minCount", "maxCount", "length", "minLength", "maxLength", "unique", "greaterThan", "greaterThanOrEqual", "lessThan", "lessThanOrEqual", "before", "after", "onOrBefore", "onOrAfter", "sha256"} {
		operatorProps[name] = jsonValue
	}
	assertion := objectSchema(operatorProps, nil)
	assertion["properties"].(map[string]any)["path"] = map[string]any{"type": "string"}
	assertion["properties"].(map[string]any)["field"] = map[string]any{"type": "string"}
	// Recursive AND/OR clauses are represented through a local definition ref.
	assertion["properties"].(map[string]any)["and"] = map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/assertionClause"}}
	assertion["properties"].(map[string]any)["or"] = map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/assertionClause"}}

	expect := objectSchema(map[string]any{
		"status": map[string]any{"oneOf": []any{
			map[string]any{"type": "integer"},
			map[string]any{"type": "array", "items": map[string]any{"type": "integer"}},
			map[string]any{"type": "string"},
		}},
		"body":    map[string]any{"$ref": "#/$defs/assertionClause"},
		"headers": map[string]any{"type": "object", "additionalProperties": objectSchema(operatorProps, nil)},
		"text":    objectSchema(operatorProps, nil),
		"binary":  objectSchema(operatorProps, nil),
		"performance": map[string]any{"type": "object", "properties": map[string]any{
			"totalMs": objectSchema(operatorProps, nil), "avgMs": objectSchema(operatorProps, nil), "p95Ms": objectSchema(operatorProps, nil), "maxMs": objectSchema(operatorProps, nil),
		}, "additionalProperties": false},
	}, nil)

	extraction := objectSchema(map[string]any{
		"from":           map[string]any{"type": "string"},
		"fromDefinition": map[string]any{"type": "string"},
		"fromHeader":     map[string]any{"type": "string"},
		"fromCookie":     map[string]any{"type": "string"},
		"fromTextRegex":  map[string]any{"type": "string"},
		"fromStatus":     boolSchema,
		"scope":          map[string]any{"type": "string", "enum": []string{"suite", "test", "step"}},
		"required":       boolSchema,
		"sensitive":      boolSchema,
	}, nil)

	step := objectSchema(map[string]any{
		"id":                map[string]any{"type": "string"},
		"name":              map[string]any{"type": "string"},
		"skip":              boolSchema,
		"continueOnFailure": boolSchema,
		"variables":         stringMap,
		"wait": objectSchema(map[string]any{
			"beforeMs": nonNegativeInt, "afterMs": nonNegativeInt, "forMs": nonNegativeInt,
		}, nil),
		"use":       map[string]any{"type": "string"},
		"with":      stringMap,
		"request":   request,
		"extend":    map[string]any{"type": "object"},
		"overrides": map[string]any{"type": "object"},
		"timeoutMs": nonNegativeInt,
		"expect":    expect,
		"extract":   map[string]any{"type": "object", "additionalProperties": extraction},
		"retry":     retry,
		"repeat": objectSchema(map[string]any{
			"warmupCount": nonNegativeInt, "count": map[string]any{"type": "integer", "minimum": 1},
		}, nil),
		"log": objectSchema(map[string]any{
			"request":  objectSchema(map[string]any{"headers": boolSchema, "body": boolSchema}, nil),
			"response": objectSchema(map[string]any{"headers": boolSchema, "body": boolSchema}, nil),
		}, nil),
		"artifacts": objectSchema(map[string]any{
			"saveResponseBodyTo": map[string]any{"type": "string"}, "saveParsedJsonTo": map[string]any{"type": "string"}, "saveHeadersTo": map[string]any{"type": "string"}, "saveTimingTo": map[string]any{"type": "string"},
		}, nil),
	}, nil)

	testCase := objectSchema(map[string]any{
		"name":      map[string]any{"type": "string"},
		"variables": stringMap,
		"skip":      boolSchema,
	}, nil)
	test := objectSchema(map[string]any{
		"id":         map[string]any{"type": "string", "pattern": `^[A-Za-z0-9][A-Za-z0-9._-]*$`},
		"name":       map[string]any{"type": "string"},
		"tags":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		"skip":       boolSchema,
		"skipReason": map[string]any{"type": "string"},
		"timeoutMs":  nonNegativeInt,
		"variables":  stringMap,
		"cases":      map[string]any{"type": "object", "propertyNames": map[string]any{"pattern": `^[A-Za-z0-9][A-Za-z0-9._-]*$`}, "additionalProperties": testCase},
		"steps":      map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/step"}},
	}, []string{"id"})

	metadataProps := map[string]any{
		"formatVersion": map[string]any{"const": SpecFormatVersion},
		"kind":          map[string]any{"type": "string"},
		"id":            map[string]any{"type": "string"},
		"name":          map[string]any{"type": "string"},
		"description":   map[string]any{"type": "string"},
	}

	requestDefinition := objectSchema(copyProps(metadataProps, map[string]any{
		"kind":    map[string]any{"const": string(RequestDefinitionKind)},
		"inputs":  map[string]any{"type": "object", "additionalProperties": inputDefinition},
		"request": map[string]any{"$ref": "#/$defs/request"},
		"outputs": map[string]any{"type": "object", "additionalProperties": outputDefinition},
	}), []string{"formatVersion", "kind", "request"})

	testSuite := objectSchema(copyProps(metadataProps, map[string]any{
		"kind":      map[string]any{"const": string(TestSuiteKind)},
		"timeoutMs": nonNegativeInt,
		"info":      objectSchema(map[string]any{"name": map[string]any{"type": "string"}, "description": map[string]any{"type": "string"}}, nil),
		"globals":   objectSchema(map[string]any{"variables": stringMap}, nil),
		"defaults": objectSchema(map[string]any{
			"timeoutMs": nonNegativeInt, "followRedirects": boolSchema, "headers": stringMap, "retry": retry,
		}, nil),
		"hooks": objectSchema(map[string]any{
			"beforeAll":  map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/step"}},
			"afterAll":   map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/step"}},
			"beforeEach": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/step"}},
			"afterEach":  map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/step"}},
		}, nil),
		"tests": map[string]any{"type": "array", "items": map[string]any{"$ref": "#/$defs/test"}},
	}), []string{"formatVersion", "kind"})

	environment := objectSchema(copyProps(metadataProps, map[string]any{
		"kind":      map[string]any{"const": string(EnvironmentKind)},
		"extends":   map[string]any{"type": "string"},
		"variables": stringMap,
	}), []string{"formatVersion", "kind"})

	return map[string]any{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id":     "https://veriflow.dev/schema/spec-1.0.json",
		"title":   "Veriflow specification 1.0",
		"oneOf": []any{
			map[string]any{"$ref": "#/$defs/requestDefinition"},
			map[string]any{"$ref": "#/$defs/testSuite"},
			map[string]any{"$ref": "#/$defs/environment"},
		},
		"$defs": map[string]any{
			"request": request, "assertionClause": assertion, "step": step, "test": test,
			"requestDefinition": requestDefinition, "testSuite": testSuite, "environment": environment,
		},
	}
}

// ProjectConfigJSONSchema returns the schema for the optional root veriflow.yml
// project configuration. It is versioned separately from test specifications
// because project defaults are an operational CLI surface, not a test document.
func ProjectConfigJSONSchema() map[string]any {
	positiveInt := map[string]any{"type": "integer", "minimum": 1}
	boolSchema := map[string]any{"type": "boolean"}
	stringSchema := map[string]any{"type": "string"}
	return map[string]any{
		"$schema":              "https://json-schema.org/draft/2020-12/schema",
		"$id":                  "https://veriflow.dev/schema/project-config-1.0.json",
		"title":                "Veriflow project configuration 1.0",
		"type":                 "object",
		"additionalProperties": false,
		"properties": map[string]any{
			"runtime": objectSchema(map[string]any{
				"runTimeoutMs": positiveInt, "suiteTimeoutMs": positiveInt, "testTimeoutMs": positiveInt,
				"cleanupTimeoutMs": positiveInt, "maxResponseBytes": positiveInt, "artifactsRoot": stringSchema,
			}, nil),
			"reports": objectSchema(map[string]any{
				"json": stringSchema, "junit": stringSchema, "consolidatedJson": stringSchema, "reportDir": stringSchema, "eventJsonl": stringSchema,
			}, nil),
			"ci": objectSchema(map[string]any{"failIfNoTests": boolSchema}, nil),
			"network": objectSchema(map[string]any{
				"caFile": stringSchema, "clientCert": stringSchema, "clientKey": stringSchema, "proxy": stringSchema,
				"cookieJar": boolSchema, "insecureSkipTlsVerify": boolSchema,
				"connectTimeoutMs":        map[string]any{"type": "integer", "minimum": 0},
				"tlsHandshakeTimeoutMs":   map[string]any{"type": "integer", "minimum": 0},
				"responseHeaderTimeoutMs": map[string]any{"type": "integer", "minimum": 0},
			}, nil),
		},
	}
}

func ProjectConfigJSONSchemaBytes() ([]byte, error) {
	return json.MarshalIndent(ProjectConfigJSONSchema(), "", "  ")
}

func SpecJSONSchemaBytes() ([]byte, error) {
	return json.MarshalIndent(SpecJSONSchema(), "", "  ")
}

func objectSchema(properties map[string]any, required []string) map[string]any {
	out := map[string]any{"type": "object", "properties": properties, "additionalProperties": false}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func copyProps(base, override map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range override {
		out[k] = v
	}
	return out
}
