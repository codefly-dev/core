package code

// ARCHITECTURE: per-file import extraction belongs to the Codefly agent that
// owns the source root. Downstream brains receive typed evidence and never
// select project file extensions or parse language syntax themselves.
//
// Go import inspection is stdlib-based and stays here so Go agents build
// without the tree-sitter CGO stack. Every other language is parsed by the
// analyzer installed via WithSemanticAnalyzer (see core/code/semantic).

import (
	"context"
	"errors"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// errNoSemanticAnalyzer marks a source-import failure caused by a missing
// analyzer (a wiring gap) rather than malformed source, so callers can report
// it as unsupported instead of a validation failure.
var errNoSemanticAnalyzer = errors.New("source-semantics analyzer not installed")

// inspectSourceImports returns a deterministic per-file import inventory. Go is
// parsed with the standard library; other languages require an installed
// semantic analyzer.
func (s *DefaultCodeServer) inspectSourceImports(ctx context.Context, root, language string) ([]*codev0.SourceFileInfo, error) {
	if language == "go" {
		return inspectGoSourceImports(ctx, s.FS, root)
	}
	if s.semantic == nil {
		return nil, fmt.Errorf("source import inspection for language %q requires a source-semantics analyzer: %w", language, errNoSemanticAnalyzer)
	}
	return s.semantic.SourceImports(ctx, s.FS, root, language)
}

// sourceImportFailureCode classifies a source-import error: a missing analyzer
// is an unsupported operation, everything else (malformed source, I/O) is a
// validation failure.
func sourceImportFailureCode(err error) basev0.FailureCode {
	if errors.Is(err, errNoSemanticAnalyzer) {
		return basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION
	}
	return basev0.FailureCode_FAILURE_CODE_VALIDATION_FAILED
}

func inspectGoSourceImports(ctx context.Context, vfs VFS, root string) ([]*codev0.SourceFileInfo, error) {
	result := make([]*codev0.SourceFileInfo, 0)
	err := vfs.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if filename != root && SkipInspectionDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) != ".go" {
			return nil
		}
		relative, err := SourceInspectionRelativePath(root, filename)
		if err != nil {
			return err
		}
		body, err := vfs.ReadFile(filename)
		if err != nil {
			return err
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), filename, body, parser.ImportsOnly)
		if err != nil {
			return fmt.Errorf("parse %s: %w", relative, err)
		}
		imports := make([]string, 0, len(parsed.Imports))
		for _, declaration := range parsed.Imports {
			value, err := strconv.Unquote(declaration.Path.Value)
			if err != nil {
				return fmt.Errorf("parse %s import %q: %w", relative, declaration.Path.Value, err)
			}
			imports = append(imports, value)
		}
		result = append(result, &codev0.SourceFileInfo{Path: relative, Imports: CanonicalImports(imports)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].GetPath() < result[j].GetPath() })
	return result, nil
}

// SourceInspectionRelativePath is the only path identity that may cross the
// source-inspection boundary. Agent-local roots are execution details: putting
// them in typed diagnostics makes identical immutable worktrees observably
// different and leaks host layout to consumers.
func SourceInspectionRelativePath(root, filename string) (string, error) {
	relative, err := filepath.Rel(root, filename)
	if err != nil {
		return "", fmt.Errorf("source inspection relative path: %w", err)
	}
	relative = filepath.ToSlash(relative)
	if relative == "." || relative == "" || relative == ".." || strings.HasPrefix(relative, "../") {
		return "", fmt.Errorf("source inspection path escapes source root")
	}
	return relative, nil
}

// SkipInspectionDir reports whether a directory is excluded from source
// inspection walks (dotfiles, dependency and build output trees).
func SkipInspectionDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true
	}
	switch strings.ToLower(name) {
	case "node_modules", "vendor", "target", "dist", "build", "__pycache__", "venv", "bin", "obj":
		return true
	default:
		return false
	}
}

// CanonicalImports returns the sorted, de-duplicated, whitespace-trimmed set of
// non-empty import paths.
func CanonicalImports(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
