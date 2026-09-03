package engine

import (
	"reflect"
	"strings"
	"testing"
)

func TestMatureYAMLParserSupportsBlockScalarsAnchorsAndMerges(t *testing.T) {
	raw, smap, err := parseYAML([]byte(`formatVersion: "1.0"
kind: requestDefinition
defaults: &defaults
  baseUrl: https://example.test
  headers:
    X-Shared: yes
request:
  <<: *defaults
  method: POST
  path: /echo
  bodyRaw: |
    first line
    second line
`))
	if err != nil {
		t.Fatal(err)
	}
	request := asMap(raw["request"])
	if request["baseUrl"] != "https://example.test" {
		t.Fatalf("merge key was not decoded: %#v", request)
	}
	if got := request["bodyRaw"]; got != "first line\nsecond line\n" {
		t.Fatalf("literal block scalar=%#v", got)
	}
	if pos := smap["$.request.bodyRaw"]; pos[0] != 11 || pos[1] <= 0 {
		t.Fatalf("source map missing bodyRaw location: %#v", pos)
	}
}

func TestMatureYAMLParserSupportsFoldedScalarUnicodeAndQuotedKeys(t *testing.T) {
	raw, _, err := parseYAML([]byte("message: >-\n  hello\n  world\n\n\"dotted.key\": \"\\u2713\"\n"))
	if err != nil {
		t.Fatal(err)
	}
	if raw["message"] != "hello world" || raw["dotted.key"] != "✓" {
		t.Fatalf("unexpected YAML values: %#v", raw)
	}
}

func TestMatureYAMLParserRejectsDuplicateKeysAndMultipleDocuments(t *testing.T) {
	if _, _, err := parseYAML([]byte("a: 1\na: 2\n")); err == nil || !strings.Contains(strings.ToLower(err.Error()), "duplicate") {
		t.Fatalf("duplicate key should fail, got %v", err)
	}
	if _, _, err := parseYAML([]byte("a: 1\n---\nb: 2\n")); err == nil || !strings.Contains(strings.ToLower(err.Error()), "multiple") {
		t.Fatalf("multiple documents should fail, got %v", err)
	}
}

func TestMatureYAMLParserKeepsJSONShapedCollections(t *testing.T) {
	raw, _, err := parseYAML([]byte("items: [one, two]\nnested: {enabled: true, count: 2}\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw["items"], []any{"one", "two"}) {
		t.Fatalf("items=%#v", raw["items"])
	}
	nested := asMap(raw["nested"])
	if nested == nil || nested["enabled"] != true || asInt(nested["count"]) != 2 {
		t.Fatalf("nested=%#v", raw["nested"])
	}
}
