package code

// ARCHITECTURE: per-file import extraction belongs to the Codefly agent that
// owns the source root. Downstream brains receive typed evidence and never
// select project file extensions or parse language syntax themselves.

import (
	"context"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	sitter "github.com/smacker/go-tree-sitter"
	tscsharp "github.com/smacker/go-tree-sitter/csharp"
	tsjava "github.com/smacker/go-tree-sitter/java"
	tskotlin "github.com/smacker/go-tree-sitter/kotlin"
	tspython "github.com/smacker/go-tree-sitter/python"
	tsrust "github.com/smacker/go-tree-sitter/rust"
	tstsx "github.com/smacker/go-tree-sitter/typescript/tsx"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

type sourceImportLanguage struct {
	extensions map[string]struct{}
	grammar    func(string) *sitter.Language
	extract    func(*sitter.Node, []byte) []string
}

var sourceImportLanguages = map[string]sourceImportLanguage{
	"python": {
		extensions: extensionSet(".py"),
		grammar:    func(string) *sitter.Language { return tspython.GetLanguage() },
		extract:    extractPythonImports,
	},
	"typescript": {
		extensions: extensionSet(".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs"),
		grammar:    func(string) *sitter.Language { return tstsx.GetLanguage() },
		extract:    extractTypeScriptImports,
	},
	"jvm": {
		extensions: extensionSet(".java", ".kt"),
		grammar: func(extension string) *sitter.Language {
			if extension == ".kt" {
				return tskotlin.GetLanguage()
			}
			return tsjava.GetLanguage()
		},
		extract: extractJVMImports,
	},
	"dotnet": {
		extensions: extensionSet(".cs"),
		grammar:    func(string) *sitter.Language { return tscsharp.GetLanguage() },
		extract:    extractCSharpImports,
	},
	"rust": {
		extensions: extensionSet(".rs"),
		grammar:    func(string) *sitter.Language { return tsrust.GetLanguage() },
		extract:    extractRustImports,
	},
}

func extensionSet(values ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

// inspectSourceImports returns a deterministic per-file inventory. A syntax
// error fails the inspection instead of silently certifying incomplete source
// evidence as authoritative.
func inspectSourceImports(ctx context.Context, vfs VFS, root, language string) ([]*codev0.SourceFileInfo, error) {
	if language == "go" {
		return inspectGoSourceImports(ctx, vfs, root)
	}
	definition, ok := sourceImportLanguages[language]
	if !ok {
		return nil, fmt.Errorf("source import inspection is unsupported for language %q", language)
	}
	result := make([]*codev0.SourceFileInfo, 0)
	err := vfs.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if filename != root && skipSourceInspectionDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		extension := strings.ToLower(filepath.Ext(entry.Name()))
		if _, wanted := definition.extensions[extension]; !wanted {
			return nil
		}
		relative, err := sourceInspectionRelativePath(root, filename)
		if err != nil {
			return err
		}
		info, err := vfs.Stat(filename)
		if err != nil {
			return err
		}
		if info.Size() > maxHashFileSize {
			return fmt.Errorf("source file %q exceeds inspection limit", relative)
		}
		body, err := vfs.ReadFile(filename)
		if err != nil {
			return err
		}
		parser := sitter.NewParser()
		parser.SetLanguage(definition.grammar(extension))
		tree, err := parser.ParseCtx(ctx, nil, body)
		parser.Close()
		if err != nil {
			return fmt.Errorf("parse %s: %w", relative, err)
		}
		rootNode := tree.RootNode()
		if rootNode.HasError() {
			tree.Close()
			return fmt.Errorf("parse %s: syntax tree contains errors", relative)
		}
		imports := canonicalImports(definition.extract(rootNode, body))
		tree.Close()
		result = append(result, &codev0.SourceFileInfo{Path: relative, Imports: imports})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].GetPath() < result[j].GetPath() })
	return result, nil
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
			if filename != root && skipSourceInspectionDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(entry.Name()) != ".go" {
			return nil
		}
		relative, err := sourceInspectionRelativePath(root, filename)
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
		result = append(result, &codev0.SourceFileInfo{Path: relative, Imports: canonicalImports(imports)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(result, func(i, j int) bool { return result[i].GetPath() < result[j].GetPath() })
	return result, nil
}

// sourceInspectionRelativePath is the only path identity that may cross the
// source-inspection boundary. Agent-local roots are execution details: putting
// them in typed diagnostics makes identical immutable worktrees observably
// different and leaks host layout to consumers.
func sourceInspectionRelativePath(root, filename string) (string, error) {
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

func skipSourceInspectionDir(name string) bool {
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

func extractPythonImports(root *sitter.Node, body []byte) []string {
	var imports []string
	walkSyntax(root, func(node *sitter.Node) {
		switch node.Type() {
		case "import_statement":
			text := strings.TrimSpace(node.Content(body))
			text = strings.TrimSpace(strings.TrimPrefix(text, "import"))
			for _, item := range strings.Split(text, ",") {
				if fields := strings.Fields(strings.TrimSpace(item)); len(fields) > 0 {
					imports = append(imports, fields[0])
				}
			}
		case "import_from_statement":
			text := strings.TrimSpace(node.Content(body))
			text = strings.TrimSpace(strings.TrimPrefix(text, "from"))
			if index := strings.Index(text, " import"); index > 0 {
				module := strings.TrimSpace(text[:index])
				if !strings.HasPrefix(module, ".") {
					imports = append(imports, module)
				}
			}
		}
	})
	return imports
}

func extractTypeScriptImports(root *sitter.Node, body []byte) []string {
	var imports []string
	walkSyntax(root, func(node *sitter.Node) {
		switch node.Type() {
		case "import_statement", "export_statement":
			if value := firstStringLiteral(node, body); validExternalImport(value) {
				imports = append(imports, value)
			}
		case "call_expression":
			function := node.ChildByFieldName("function")
			if function != nil && !function.IsNull() && strings.TrimSpace(function.Content(body)) == "require" {
				if value := firstStringLiteral(node, body); validExternalImport(value) {
					imports = append(imports, value)
				}
			}
		}
	})
	return imports
}

func extractJVMImports(root *sitter.Node, body []byte) []string {
	var imports []string
	walkSyntax(root, func(node *sitter.Node) {
		if node.Type() != "import_declaration" && node.Type() != "import_header" {
			return
		}
		value := strings.TrimSpace(node.Content(body))
		value = strings.TrimSpace(strings.TrimPrefix(value, "import"))
		value = strings.TrimSpace(strings.TrimPrefix(value, "static"))
		value = strings.TrimSuffix(value, ";")
		if index := strings.Index(value, " as "); index > 0 {
			value = value[:index]
		}
		if value != "" {
			imports = append(imports, strings.TrimSpace(value))
		}
	})
	return imports
}

func extractCSharpImports(root *sitter.Node, body []byte) []string {
	var imports []string
	walkSyntax(root, func(node *sitter.Node) {
		if node.Type() != "using_directive" {
			return
		}
		value := strings.TrimSpace(node.Content(body))
		value = strings.TrimSpace(strings.TrimPrefix(value, "global"))
		value = strings.TrimSpace(strings.TrimPrefix(value, "using"))
		value = strings.TrimSpace(strings.TrimPrefix(value, "static"))
		value = strings.TrimSuffix(value, ";")
		if _, rhs, alias := strings.Cut(value, "="); alias {
			value = rhs
		}
		if value != "" {
			imports = append(imports, strings.TrimSpace(value))
		}
	})
	return imports
}

func walkSyntax(node *sitter.Node, visit func(*sitter.Node)) {
	if node == nil || node.IsNull() {
		return
	}
	visit(node)
	for index := 0; index < int(node.NamedChildCount()); index++ {
		walkSyntax(node.NamedChild(index), visit)
	}
}

func firstStringLiteral(node *sitter.Node, body []byte) string {
	value := ""
	walkSyntax(node, func(candidate *sitter.Node) {
		if value != "" || (candidate.Type() != "string" && candidate.Type() != "string_literal") {
			return
		}
		literal := strings.TrimSpace(candidate.Content(body))
		if unquoted, err := strconv.Unquote(literal); err == nil {
			value = unquoted
		} else {
			value = strings.Trim(literal, "'\"")
		}
	})
	return value
}

func validExternalImport(value string) bool {
	value = strings.TrimSpace(value)
	return value != "" && !strings.HasPrefix(value, ".") && !strings.HasPrefix(value, "/")
}

func canonicalImports(values []string) []string {
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
