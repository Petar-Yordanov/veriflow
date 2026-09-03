package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func absPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve absolute path %q: %w", path, err)
	}
	return filepath.Clean(abs), nil
}

// ensureExistingPathWithinRoot verifies both lexical containment and the real
// filesystem target. This prevents a file below the project tree from escaping
// through a symlink that points outside the project.
func ensureExistingPathWithinRoot(root, path, kind string) (string, error) {
	rootAbs, err := absPath(root)
	if err != nil {
		return "", fmt.Errorf("resolve %s root: %w", kind, err)
	}
	pathAbs, err := absPath(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s path: %w", kind, err)
	}
	if !pathWithinRoot(rootAbs, pathAbs) {
		return "", fmt.Errorf("%s escapes root: %s", kind, path)
	}
	realRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", fmt.Errorf("resolve %s root: %w", kind, err)
	}
	realPath, err := filepath.EvalSymlinks(pathAbs)
	if err != nil {
		return "", err
	}
	if !pathWithinRoot(filepath.Clean(realRoot), filepath.Clean(realPath)) {
		return "", fmt.Errorf("%s escapes root through symlink: %s", kind, path)
	}
	return pathAbs, nil
}

// ensureWritablePathWithinRoot is for output paths which may not exist yet.
// Existing descendant components are not allowed to be symlinks, preventing an
// artifacts/foo -> /outside link from redirecting writes outside the root.
func ensureWritablePathWithinRoot(root, path, kind string) (string, error) {
	rootAbs, err := absPath(root)
	if err != nil {
		return "", fmt.Errorf("resolve %s root: %w", kind, err)
	}
	pathAbs, err := absPath(path)
	if err != nil {
		return "", fmt.Errorf("resolve %s path: %w", kind, err)
	}
	if !pathWithinRoot(rootAbs, pathAbs) {
		return "", fmt.Errorf("%s escapes root: %s", kind, path)
	}
	rel, err := filepath.Rel(rootAbs, pathAbs)
	if err != nil {
		return "", err
	}
	cur := rootAbs
	parts := strings.Split(rel, string(filepath.Separator))
	for _, part := range parts[:max(0, len(parts)-1)] {
		if part == "" || part == "." {
			continue
		}
		cur = filepath.Join(cur, part)
		info, err := os.Lstat(cur)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%s path contains symlink: %s", kind, cur)
		}
	}
	return pathAbs, nil
}

func pathWithinRoot(root, path string) bool {
	if root == path {
		return true
	}
	return strings.HasPrefix(path, root+string(filepath.Separator))
}
