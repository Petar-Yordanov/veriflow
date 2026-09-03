package engine

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"

	yaml "go.yaml.in/yaml/v3"
)

const (
	maxYAMLDepth   = 128
	maxYAMLAliases = 1024
)

// parseYAML uses the YAML organization's mature v3 parser for YAML syntax and
// keeps Veriflow's own safety/diagnostic layer on top: single-document input,
// duplicate-key rejection, bounded nesting/aliases, string mapping keys, and a
// source map used by semantic validation.
func parseYAML(data []byte) (map[string]any, map[string][2]int, error) {
	if !utf8.Valid(data) {
		return nil, nil, fmt.Errorf("invalid UTF-8 input")
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nil, nil, err
	}
	if len(doc.Content) == 0 {
		return map[string]any{}, map[string][2]int{"$": {1, 1}}, nil
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != nil && !errors.Is(err, io.EOF) {
		return nil, nil, err
	} else if err == nil && len(extra.Content) > 0 {
		return nil, nil, fmt.Errorf("multiple YAML documents are not supported (line %d:%d)", extra.Line, extra.Column)
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, nil, fmt.Errorf("top-level YAML value must be a mapping (line %d:%d)", root.Line, root.Column)
	}
	aliases := 0
	if err := validateYAMLNode(root, 0, &aliases); err != nil {
		return nil, nil, err
	}
	smap := map[string][2]int{"$": {root.Line, root.Column}}
	buildYAMLSourceMap(root, "$", smap, map[*yaml.Node]bool{})
	var raw map[string]any
	if err := root.Decode(&raw); err != nil {
		return nil, nil, err
	}
	normalized, err := normalizeYAMLValue(raw)
	if err != nil {
		return nil, nil, err
	}
	out, ok := normalized.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("top-level YAML value must be a mapping")
	}
	return out, smap, nil
}

func validateYAMLNode(node *yaml.Node, depth int, aliases *int) error {
	if node == nil {
		return nil
	}
	if depth > maxYAMLDepth {
		return fmt.Errorf("YAML nesting depth exceeds %d at line %d:%d", maxYAMLDepth, node.Line, node.Column)
	}
	switch node.Kind {
	case yaml.AliasNode:
		*aliases = *aliases + 1
		if *aliases > maxYAMLAliases {
			return fmt.Errorf("YAML alias count exceeds %d at line %d:%d", maxYAMLAliases, node.Line, node.Column)
		}
		return validateYAMLNode(node.Alias, depth+1, aliases)
	case yaml.MappingNode:
		seen := map[string]struct{}{}
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Kind != yaml.ScalarNode {
				return fmt.Errorf("YAML mapping keys must be scalars at line %d:%d", key.Line, key.Column)
			}
			// Veriflow specs are JSON-shaped and deliberately require string keys.
			if key.ShortTag() != "!!str" && key.Value != "<<" {
				return fmt.Errorf("YAML mapping key %q must be a string at line %d:%d", key.Value, key.Line, key.Column)
			}
			if _, exists := seen[key.Value]; exists {
				return fmt.Errorf("duplicate YAML key %q at line %d:%d", key.Value, key.Line, key.Column)
			}
			seen[key.Value] = struct{}{}
			if err := validateYAMLNode(value, depth+1, aliases); err != nil {
				return err
			}
		}
	case yaml.SequenceNode:
		for _, child := range node.Content {
			if err := validateYAMLNode(child, depth+1, aliases); err != nil {
				return err
			}
		}
	}
	return nil
}

func buildYAMLSourceMap(node *yaml.Node, path string, smap map[string][2]int, stack map[*yaml.Node]bool) {
	if node == nil || stack[node] {
		return
	}
	stack[node] = true
	defer delete(stack, node)
	if node.Kind == yaml.AliasNode {
		buildYAMLSourceMap(node.Alias, path, smap, stack)
		return
	}
	switch node.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, value := node.Content[i], node.Content[i+1]
			if key.Value == "<<" {
				continue
			}
			childPath := path + "." + key.Value
			smap[childPath] = [2]int{key.Line, key.Column}
			buildYAMLSourceMap(value, childPath, smap, stack)
		}
	case yaml.SequenceNode:
		for i, child := range node.Content {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			smap[childPath] = [2]int{child.Line, child.Column}
			buildYAMLSourceMap(child, childPath, smap, stack)
		}
	}
}

func normalizeYAMLValue(v any) (any, error) {
	switch x := v.(type) {
	case nil, string, bool, int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
		return x, nil
	case time.Time:
		return x.UTC().Format(time.RFC3339Nano), nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, value := range x {
			n, err := normalizeYAMLValue(value)
			if err != nil {
				return nil, err
			}
			out[k] = n
		}
		return out, nil
	case map[any]any:
		out := make(map[string]any, len(x))
		for key, value := range x {
			ks, ok := key.(string)
			if !ok {
				return nil, fmt.Errorf("YAML mapping key %v is not a string", key)
			}
			n, err := normalizeYAMLValue(value)
			if err != nil {
				return nil, err
			}
			out[ks] = n
		}
		return out, nil
	case []any:
		out := make([]any, len(x))
		for i, value := range x {
			n, err := normalizeYAMLValue(value)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported YAML value type %T", v)
	}
}

// yamlErrorLine normalizes parser errors for stable diagnostic classification.
func yamlErrorLine(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}
