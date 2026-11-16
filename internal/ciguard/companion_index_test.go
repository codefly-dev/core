package ciguard

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Core compiles no companion image version in. Which images a run uses is the
// caller's data (companions/index); a go:embed of a companion recipe would make
// every companion release a core release again, and every agent a re-pin.
func TestCoreEmbedsNoCompanionVersion(t *testing.T) {
	root := repoRoot(t)
	err := filepath.WalkDir(filepath.Join(root, "companions"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "vendor" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, line := range strings.Split(string(content), "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), "//go:embed") && strings.Contains(line, "info.codefly.yaml") {
				relative, _ := filepath.Rel(root, path)
				t.Errorf("%s embeds a companion version (%s); companion versions are the caller's companion index", relative, strings.TrimSpace(line))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
