package golang

// workspace.go owns discovery of source-root Go workspaces. A go.work that
// lives at the attached source root is project configuration, not an ambient
// parent-workspace preference: the runtime must honor it even when
// with-workspace=false asks the agent to ignore unrelated parent workspaces.

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/mod/modfile"
)

type sourceGoWorkspace struct {
	root           string
	moduleDirs     []string
	packageTargets []string
}

// loadSourceGoWorkspace parses a go.work owned by sourceDir and resolves its
// use directives into bounded module directories and package patterns. The
// source boundary is intentional: a runtime attached to one project must not
// silently execute packages reached through an escaping use path.
func loadSourceGoWorkspace(sourceDir string) (*sourceGoWorkspace, bool, error) {
	workPath := filepath.Join(sourceDir, "go.work")
	data, err := os.ReadFile(workPath)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, true, fmt.Errorf("read %s: %w", workPath, err)
	}
	work, err := modfile.ParseWork(workPath, data, nil)
	if err != nil {
		return nil, true, fmt.Errorf("parse %s: %w", workPath, err)
	}

	root, err := canonicalExistingDir(sourceDir)
	if err != nil {
		return nil, true, fmt.Errorf("resolve Go workspace root %s: %w", sourceDir, err)
	}
	result := &sourceGoWorkspace{root: root}
	seen := make(map[string]struct{}, len(work.Use))
	for _, use := range work.Use {
		if use == nil || strings.TrimSpace(use.Path) == "" {
			continue
		}
		modulePath := use.Path
		if !filepath.IsAbs(modulePath) {
			modulePath = filepath.Join(root, modulePath)
		}
		moduleDir, err := canonicalExistingDir(modulePath)
		if err != nil {
			return nil, true, fmt.Errorf("resolve go.work use %q: %w", use.Path, err)
		}
		rel, err := filepath.Rel(root, moduleDir)
		if err != nil {
			return nil, true, fmt.Errorf("relativize go.work use %q: %w", use.Path, err)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, true, fmt.Errorf("go.work use %q escapes attached source root %s", use.Path, root)
		}
		if _, duplicate := seen[moduleDir]; duplicate {
			continue
		}
		if _, err := os.Stat(filepath.Join(moduleDir, "go.mod")); err != nil {
			return nil, true, fmt.Errorf("go.work use %q does not contain a readable go.mod: %w", use.Path, err)
		}

		target := "./..."
		if rel != "." {
			target = "./" + filepath.ToSlash(rel) + "/..."
		}
		seen[moduleDir] = struct{}{}
		result.moduleDirs = append(result.moduleDirs, moduleDir)
		result.packageTargets = append(result.packageTargets, target)
	}
	if len(result.moduleDirs) == 0 {
		return nil, true, fmt.Errorf("go.work at %s declares no usable modules", workPath)
	}
	return result, true, nil
}

func canonicalExistingDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", resolved)
	}
	return filepath.Clean(resolved), nil
}
