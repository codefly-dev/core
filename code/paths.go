package code

import (
	"fmt"
	"path/filepath"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"
)

// resolvePath confines an RPC-supplied path to root. In addition to rejecting
// lexical traversal and absolute paths, SecureJoin resolves existing symlinks
// as though root were a filesystem root. This prevents an in-tree symlink from
// turning an otherwise relative request into an operation outside the source
// tree.
func resolvePath(root, requested string) (string, error) {
	if filepath.IsAbs(requested) {
		return "", fmt.Errorf("absolute paths are not allowed: %q", requested)
	}

	rootClean := filepath.Clean(root)
	rootAbs, err := filepath.Abs(rootClean)
	if err != nil {
		return "", fmt.Errorf("resolve source root: %w", err)
	}
	rootAbs = filepath.Clean(rootAbs)

	cleaned := filepath.Clean(requested)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes source root: %q", requested)
	}

	resolved, err := securejoin.SecureJoin(rootAbs, cleaned)
	if err != nil {
		return "", fmt.Errorf("resolve path %q: %w", requested, err)
	}
	if !isWithin(resolved, rootAbs) {
		return "", fmt.Errorf("path escapes source root: %q", requested)
	}
	// Preserve the caller's absolute-vs-relative root representation. Several
	// VFS implementations key overlay entries by the exact cleaned path form;
	// silently converting a relative SourceDir to absolute would split their
	// view of the same tree.
	rel, err := filepath.Rel(rootAbs, resolved)
	if err != nil {
		return "", fmt.Errorf("make resolved path relative to source root: %w", err)
	}
	return filepath.Join(rootClean, rel), nil
}

// isWithin returns true when path is root itself or one of its descendants.
func isWithin(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
