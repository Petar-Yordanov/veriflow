package engine

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// ValidateRequestInputs enforces the request-definition input contract after
// variables have been resolved. It is intentionally runtime validation because
// values may come from environments, previous extractions, or CLI overlays.
func ValidateRequestInputs(def RequestDefinitionSpec, values map[string]any) error {
	if values == nil {
		values = map[string]any{}
	}
	unknown := []string{}
	for name := range values {
		if _, ok := def.Inputs[name]; !ok {
			unknown = append(unknown, name)
		}
	}
	sort.Strings(unknown)
	if len(unknown) > 0 {
		return fmt.Errorf("unknown input(s) for request %q: %s", requestDisplayName(def), strings.Join(unknown, ", "))
	}

	names := make([]string, 0, len(def.Inputs))
	for name := range def.Inputs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		input := def.Inputs[name]
		value, ok := values[name]
		if !ok {
			if input.Required && input.Default == nil {
				return fmt.Errorf("required input %q is missing for request %q", name, requestDisplayName(def))
			}
			continue
		}
		if input.Type != "" && input.Type != "any" && !inputTypeMatches(input.Type, value) {
			return fmt.Errorf("input %q for request %q must be %s, got %s", name, requestDisplayName(def), input.Type, typeName(value))
		}
	}
	return nil
}

func requestDisplayName(def RequestDefinitionSpec) string {
	if def.Metadata.ID != "" {
		return def.Metadata.ID
	}
	if def.Metadata.Name != "" {
		return def.Metadata.Name
	}
	return def.Path
}

func inputTypeMatches(kind string, value any) bool {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "string":
		_, ok := value.(string)
		return ok
	case "number":
		_, ok := asFloat(value)
		return ok
	case "integer", "int":
		switch v := value.(type) {
		case int, int64:
			return true
		case float64:
			return v == float64(int64(v))
		case json.Number:
			_, err := v.Int64()
			return err == nil
		default:
			return false
		}
	case "boolean", "bool":
		_, ok := value.(bool)
		return ok
	case "object", "map":
		_, ok := value.(map[string]any)
		return ok
	case "array", "list":
		_, ok := value.([]any)
		return ok
	case "null":
		return value == nil
	default:
		return false
	}
}
