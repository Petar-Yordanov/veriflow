package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func saveArtifact(root, rel string, data []byte) (string, error) {
	if rel == "" {
		return "", nil
	}
	rootAbs, err := absPath(root)
	if err != nil {
		return "", err
	}
	p, err := ensureWritablePathWithinRoot(rootAbs, filepath.Join(rootAbs, rel), "artifact")
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, data, 0644); err != nil {
		return "", err
	}
	return p, nil
}
func saveJSONArtifact(root, rel string, v any) (string, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", err
	}
	return saveArtifact(root, rel, b)
}
