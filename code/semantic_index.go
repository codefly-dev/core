package code

// ARCHITECTURE: semantic projection executes inside Codefly because only the
// agent boundary may inspect project bytes. The result is deliberately
// language-neutral and body-free so orchestration clients can reconcile graph
// facts without acquiring a parser, choosing extensions, or caching source.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	sitter "github.com/smacker/go-tree-sitter"
	tscsharp "github.com/smacker/go-tree-sitter/csharp"
	tsgo "github.com/smacker/go-tree-sitter/golang"
	tsjava "github.com/smacker/go-tree-sitter/java"
	tskotlin "github.com/smacker/go-tree-sitter/kotlin"
	tspython "github.com/smacker/go-tree-sitter/python"
	tstsx "github.com/smacker/go-tree-sitter/typescript/tsx"
)

const semanticAnalyzerVersion = "codefly.semantic-index/v3"

type semanticLanguage struct {
	name       string
	grammar    *sitter.Language
	extensions map[string]struct{}
}

var semanticLanguages = []semanticLanguage{
	{name: "go", grammar: tsgo.GetLanguage(), extensions: extensionSet(".go")},
	{name: "python", grammar: tspython.GetLanguage(), extensions: extensionSet(".py")},
	{name: "typescript", grammar: tstsx.GetLanguage(), extensions: extensionSet(".ts", ".tsx", ".mts", ".cts", ".js", ".jsx", ".mjs", ".cjs")},
	{name: "java", grammar: tsjava.GetLanguage(), extensions: extensionSet(".java")},
	{name: "kotlin", grammar: tskotlin.GetLanguage(), extensions: extensionSet(".kt", ".kts")},
	{name: "csharp", grammar: tscsharp.GetLanguage(), extensions: extensionSet(".cs")},
}

const (
	semanticFunction  = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_FUNCTION
	semanticMethod    = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_METHOD
	semanticClass     = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_CLASS
	semanticStruct    = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_STRUCT
	semanticInterface = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_INTERFACE
	semanticEnum      = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_ENUM
	semanticVariable  = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_VARIABLE
	semanticConstant  = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_CONSTANT
	semanticAlias     = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_TYPE_ALIAS
)

var semanticDeclarationKinds = map[string]map[string]basev0.SemanticSymbolKind{
	"go":         {"function_declaration": semanticFunction, "method_declaration": semanticMethod, "type_spec": semanticAlias, "const_spec": semanticConstant, "var_spec": semanticVariable},
	"python":     {"function_definition": semanticFunction, "class_definition": semanticClass, "assignment": semanticVariable},
	"typescript": {"function_declaration": semanticFunction, "method_definition": semanticMethod, "class_declaration": semanticClass, "abstract_class_declaration": semanticClass, "interface_declaration": semanticInterface, "enum_declaration": semanticEnum, "type_alias_declaration": semanticAlias, "lexical_declaration": semanticVariable, "variable_declaration": semanticVariable},
	"java":       {"method_declaration": semanticMethod, "constructor_declaration": semanticMethod, "class_declaration": semanticClass, "record_declaration": semanticClass, "interface_declaration": semanticInterface, "enum_declaration": semanticEnum, "field_declaration": semanticVariable},
	"kotlin":     {"function_declaration": semanticMethod, "class_declaration": semanticClass, "object_declaration": semanticClass, "property_declaration": semanticVariable, "type_alias": semanticAlias},
	"csharp":     {"method_declaration": semanticMethod, "constructor_declaration": semanticMethod, "class_declaration": semanticClass, "record_declaration": semanticClass, "interface_declaration": semanticInterface, "struct_declaration": semanticStruct, "enum_declaration": semanticEnum, "field_declaration": semanticVariable, "property_declaration": semanticVariable},
}

// getSemanticIndex performs one deterministic scan through the server VFS.
// Parse failures degrade individual files while retaining valid evidence from
// the rest of the code unit. An unsupported unit is explicit, never an empty
// response that a caller could mistake for complete coverage.
func (s *DefaultCodeServer) getSemanticIndex(ctx context.Context, _ *codev0.GetSemanticIndexRequest) (*codev0.CodeResponse, error) {
	index := &basev0.SemanticIndex{
		State:           basev0.SemanticIndexState_SEMANTIC_INDEX_STATE_NOT_ATTEMPTED,
		Analyzer:        "codefly-core/tree-sitter",
		AnalyzerVersion: semanticAnalyzerVersion,
	}
	err := s.FS.WalkDir(s.SourceDir, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if filename != s.SourceDir && skipSourceInspectionDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		definition, ok := semanticLanguageForExtension(strings.ToLower(filepath.Ext(entry.Name())))
		if !ok {
			return nil
		}
		relative, err := filepath.Rel(s.SourceDir, filename)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		info, err := s.FS.Stat(filename)
		if err != nil {
			return err
		}
		if info.Size() > maxHashFileSize {
			index.Issues = append(index.Issues, semanticIssue("file_too_large", relative, fmt.Sprintf("source file exceeds %d-byte semantic inspection limit", maxHashFileSize)))
			return nil
		}
		body, err := s.FS.ReadFile(filename)
		if err != nil {
			index.Issues = append(index.Issues, semanticIssue("read_failed", relative, err.Error()))
			return nil
		}
		file, symbols, err := projectSemanticFile(ctx, definition, relative, body)
		if file != nil {
			index.Files = append(index.Files, file)
		}
		if err != nil {
			index.Issues = append(index.Issues, semanticIssue("parse_failed", relative, err.Error()))
			return nil
		}
		index.Symbols = append(index.Symbols, symbols...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("semantic index: %w", err)
	}
	seenLanguages := make(map[string]struct{})
	for _, file := range index.Files {
		seenLanguages[file.GetLanguage()] = struct{}{}
	}
	for language := range seenLanguages {
		index.Languages = append(index.Languages, language)
	}
	sort.Strings(index.Languages)
	sort.Slice(index.Files, func(i, j int) bool { return index.Files[i].GetPath() < index.Files[j].GetPath() })
	sort.Slice(index.Symbols, func(i, j int) bool {
		left, right := index.Symbols[i], index.Symbols[j]
		if left.GetLocation().GetPath() != right.GetLocation().GetPath() {
			return left.GetLocation().GetPath() < right.GetLocation().GetPath()
		}
		if left.GetLocation().GetStartLine() != right.GetLocation().GetStartLine() {
			return left.GetLocation().GetStartLine() < right.GetLocation().GetStartLine()
		}
		return left.GetQualifiedName() < right.GetQualifiedName()
	})
	sort.Slice(index.Issues, func(i, j int) bool {
		if index.Issues[i].GetPath() != index.Issues[j].GetPath() {
			return index.Issues[i].GetPath() < index.Issues[j].GetPath()
		}
		return index.Issues[i].GetCode() < index.Issues[j].GetCode()
	})
	switch {
	case len(index.Files) == 0 && len(index.Issues) == 0:
		index.Issues = append(index.Issues, semanticIssue("unsupported_source", "", "no supported semantic source files were found"))
	case len(index.Issues) > 0:
		index.State = basev0.SemanticIndexState_SEMANTIC_INDEX_STATE_DEGRADED
	default:
		index.State = basev0.SemanticIndexState_SEMANTIC_INDEX_STATE_COMPLETE
	}
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_GetSemanticIndex{GetSemanticIndex: index}}, nil
}

func semanticLanguageForExtension(extension string) (semanticLanguage, bool) {
	for _, definition := range semanticLanguages {
		if _, ok := definition.extensions[extension]; ok {
			return definition, true
		}
	}
	return semanticLanguage{}, false
}

func semanticIssue(code, path, message string) *basev0.SemanticIssue {
	return &basev0.SemanticIssue{Code: code, Path: path, Message: message}
}

func projectSemanticFile(ctx context.Context, definition semanticLanguage, path string, body []byte) (*basev0.SemanticFile, []*basev0.SemanticSymbol, error) {
	file := &basev0.SemanticFile{
		Path: path, ContentSha256: semanticHash(body), ByteSize: int64(len(body)), Language: definition.name,
	}
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(definition.grammar)
	tree, err := parser.ParseCtx(ctx, nil, body)
	if err != nil {
		return file, nil, err
	}
	defer tree.Close()
	root := tree.RootNode()
	file.Imports = semanticImports(definition.name, path, root, body)
	if root.HasError() {
		return file, nil, fmt.Errorf("syntax tree contains errors")
	}
	packageName := semanticPackage(definition.name, path, root, body)
	var symbols []*basev0.SemanticSymbol
	projectSemanticDeclarations(definition.name, path, packageName, "", false, root, body, &symbols)
	return file, symbols, nil
}

func projectSemanticDeclarations(language, path, packageName, parent string, inCallable bool, node *sitter.Node, body []byte, out *[]*basev0.SemanticSymbol) {
	if node == nil || node.IsNull() {
		return
	}
	declarations := semanticDeclarations(language, node, body, inCallable)
	nextParent := parent
	nextInCallable := inCallable
	for _, declaration := range declarations {
		name := strings.TrimSpace(declaration.name)
		if name == "" {
			continue
		}
		qualified := qualifySemanticName(packageName, parent, name)
		if language == "go" && declaration.kind == basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_METHOD {
			if receiver := goReceiverName(node, body); receiver != "" {
				qualified = qualifySemanticName(packageName, "", receiver+"."+name)
			}
		}
		bodyNode := semanticBodyNode(node)
		signatureBytes := semanticSignatureBytes(node, bodyNode, body, declaration.kind, name)
		bodyBytes := []byte(nil)
		if bodyNode != nil && !bodyNode.IsNull() {
			bodyBytes = nodeBytes(bodyNode, body)
		} else if semanticDataDeclaration(declaration.kind) {
			bodyBytes = nodeBytes(node, body)
		}
		symbol := &basev0.SemanticSymbol{
			Name: name, QualifiedName: qualified, Kind: declaration.kind,
			Location: semanticLocation(path, node), Package: packageName,
			ParentQualifiedName: parent, Signature: boundedSignature(signatureBytes),
			SignatureSha256: semanticHash(signatureBytes),
		}
		if len(bodyBytes) > 0 {
			symbol.BodySha256 = semanticHash(bodyBytes)
		}
		if semanticCallable(declaration.kind) {
			symbol.Calls = semanticUses(language, path, node, body, true)
			symbol.References = semanticUses(language, path, node, body, false)
		}
		*out = append(*out, symbol)
		if semanticContainer(declaration.kind) || semanticCallable(declaration.kind) {
			nextParent = qualified
		}
		if semanticCallable(declaration.kind) {
			nextInCallable = true
		}
	}
	for index := 0; index < int(node.NamedChildCount()); index++ {
		child := node.NamedChild(index)
		if len(declarations) > 0 && isNestedSemanticBody(node, child) {
			projectSemanticDeclarations(language, path, packageName, nextParent, nextInCallable, child, body, out)
			continue
		}
		projectSemanticDeclarations(language, path, packageName, parent, inCallable, child, body, out)
	}
}

type semanticDeclaration struct {
	name string
	kind basev0.SemanticSymbolKind
}

func semanticDeclarations(language string, node *sitter.Node, body []byte, inCallable bool) []semanticDeclaration {
	kind, ok := semanticDeclarationKind(language, node.Type())
	if !ok {
		return nil
	}
	if semanticDataDeclaration(kind) && inCallable {
		return nil
	}
	if language == "go" && node.Type() == "type_spec" {
		if declaredType := node.ChildByFieldName("type"); declaredType != nil && !declaredType.IsNull() {
			switch declaredType.Type() {
			case "struct_type":
				kind = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_STRUCT
			case "interface_type":
				kind = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_INTERFACE
			}
		}
	}
	if language == "python" && node.Type() == "function_definition" && insideSemanticContainer(node) {
		kind = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_METHOD
	}
	if language == "kotlin" && node.Type() == "function_declaration" && !insideSemanticContainer(node) {
		kind = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_FUNCTION
	}
	names := semanticDeclarationNames(node, body)
	result := make([]semanticDeclaration, 0, len(names))
	for _, name := range names {
		result = append(result, semanticDeclaration{name: name, kind: kind})
	}
	return result
}

func semanticDeclarationKind(language, nodeType string) (basev0.SemanticSymbolKind, bool) {
	kind, ok := semanticDeclarationKinds[language][nodeType]
	return kind, ok
}

func semanticDeclarationNames(node *sitter.Node, body []byte) []string {
	if name := node.ChildByFieldName("name"); name != nil && !name.IsNull() {
		return []string{strings.TrimSpace(name.Content(body))}
	}
	if node.Type() == "assignment" {
		if left := node.ChildByFieldName("left"); left != nil && !left.IsNull() && (left.Type() == "identifier" || left.Type() == "attribute") {
			return []string{strings.TrimSpace(left.Content(body))}
		}
	}
	// Older tree-sitter Kotlin grammars expose declaration identifiers as an
	// unnamed direct simple_identifier rather than a named field.
	for index := 0; index < int(node.NamedChildCount()); index++ {
		candidate := node.NamedChild(index)
		if candidate.Type() == "simple_identifier" || candidate.Type() == "type_identifier" || candidate.Type() == "identifier" {
			return []string{strings.TrimSpace(candidate.Content(body))}
		}
	}
	var names []string
	walkSyntax(node, func(candidate *sitter.Node) {
		if candidate == node {
			return
		}
		switch candidate.Type() {
		case "variable_declarator", "variable_declaration", "const_spec", "var_spec":
			if name := candidate.ChildByFieldName("name"); name != nil && !name.IsNull() {
				names = append(names, strings.TrimSpace(name.Content(body)))
			}
		}
	})
	return canonicalImports(names)
}

func semanticBodyNode(node *sitter.Node) *sitter.Node {
	for _, field := range []string{"body", "value"} {
		if child := node.ChildByFieldName(field); child != nil && !child.IsNull() {
			return child
		}
	}
	if node.Type() == "type_spec" {
		if child := node.ChildByFieldName("type"); child != nil && !child.IsNull() {
			return child
		}
	}
	return nil
}

func semanticSignatureBytes(node, bodyNode *sitter.Node, body []byte, kind basev0.SemanticSymbolKind, name string) []byte {
	if semanticDataDeclaration(kind) || (semanticCallable(kind) && (bodyNode == nil || bodyNode.IsNull())) {
		return []byte(name)
	}
	start, end := int(node.StartByte()), int(node.EndByte())
	if bodyNode != nil && !bodyNode.IsNull() {
		end = int(bodyNode.StartByte())
	}
	if start < 0 || end < start || end > len(body) {
		return nil
	}
	return []byte(strings.TrimSpace(string(body[start:end])))
}

func nodeBytes(node *sitter.Node, body []byte) []byte {
	start, end := int(node.StartByte()), int(node.EndByte())
	if start < 0 || end < start || end > len(body) {
		return nil
	}
	return body[start:end]
}

func boundedSignature(value []byte) string {
	const maxSignatureBytes = 16 * 1024
	if len(value) > maxSignatureBytes {
		value = value[:maxSignatureBytes]
	}
	return string(value)
}

func semanticHash(value []byte) string {
	digest := sha256.Sum256(value)
	return hex.EncodeToString(digest[:])
}

func semanticLocation(path string, node *sitter.Node) *basev0.SemanticLocation {
	start, end := node.StartPoint(), node.EndPoint()
	return &basev0.SemanticLocation{
		Path: path, StartLine: int32(start.Row + 1), StartColumn: int32(start.Column + 1),
		EndLine: int32(end.Row + 1), EndColumn: int32(end.Column + 1),
	}
}

func semanticPackage(language, path string, root *sitter.Node, body []byte) string {
	var packageName string
	walkSyntax(root, func(node *sitter.Node) {
		if packageName != "" {
			return
		}
		switch node.Type() {
		case "package_clause", "package_declaration", "package_header", "namespace_declaration", "file_scoped_namespace_declaration":
			if name := node.ChildByFieldName("name"); name != nil && !name.IsNull() {
				packageName = strings.TrimSpace(name.Content(body))
				return
			}
			value := strings.TrimSpace(node.Content(body))
			for _, prefix := range []string{"package", "namespace"} {
				value = strings.TrimSpace(strings.TrimPrefix(value, prefix))
			}
			packageName = strings.TrimSuffix(value, ";")
		}
	})
	if packageName != "" {
		return packageName
	}
	module := strings.TrimSuffix(filepath.ToSlash(path), filepath.Ext(path))
	module = strings.TrimSuffix(module, "/index")
	return strings.ReplaceAll(module, "/", ".")
}

func qualifySemanticName(packageName, parent, name string) string {
	if parent != "" {
		return parent + "." + name
	}
	if packageName != "" {
		return packageName + "." + name
	}
	return name
}

func semanticCallable(kind basev0.SemanticSymbolKind) bool {
	return kind == basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_FUNCTION || kind == basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_METHOD
}

func semanticDataDeclaration(kind basev0.SemanticSymbolKind) bool {
	switch kind {
	case basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_FIELD,
		basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_VARIABLE,
		basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_CONSTANT:
		return true
	default:
		return false
	}
}

func semanticContainer(kind basev0.SemanticSymbolKind) bool {
	switch kind {
	case basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_CLASS,
		basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_STRUCT,
		basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_INTERFACE,
		basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_ENUM:
		return true
	default:
		return false
	}
}

func isNestedSemanticBody(parent, child *sitter.Node) bool {
	body := semanticBodyNode(parent)
	return body != nil && !body.IsNull() && body.StartByte() == child.StartByte() && body.EndByte() == child.EndByte()
}

func insideSemanticContainer(node *sitter.Node) bool {
	for parent := node.Parent(); parent != nil && !parent.IsNull(); parent = parent.Parent() {
		if kind, ok := semanticDeclarationKind("python", parent.Type()); ok && semanticContainer(kind) {
			return true
		}
		if kind, ok := semanticDeclarationKind("kotlin", parent.Type()); ok && semanticContainer(kind) {
			return true
		}
	}
	return false
}

func goReceiverName(node *sitter.Node, body []byte) string {
	receiver := node.ChildByFieldName("receiver")
	if receiver == nil || receiver.IsNull() {
		return ""
	}
	result := ""
	walkSyntax(receiver, func(candidate *sitter.Node) {
		if candidate.Type() == "type_identifier" {
			result = strings.TrimSpace(candidate.Content(body))
		}
	})
	return result
}

func semanticUses(language, path string, declaration *sitter.Node, body []byte, calls bool) []*basev0.SemanticUse {
	seen := make(map[string]struct{})
	var result []*basev0.SemanticUse
	var visit func(*sitter.Node, bool)
	visit = func(node *sitter.Node, root bool) {
		if node == nil || node.IsNull() {
			return
		}
		if !root {
			if _, nested := semanticDeclarationKind(language, node.Type()); nested {
				return
			}
		}
		var name string
		if calls {
			name = semanticCallName(node, body)
		} else if semanticTypeNode(node.Type()) {
			name = cleanSemanticName(node.Content(body))
		}
		if name == "" {
			for index := 0; index < int(node.NamedChildCount()); index++ {
				visit(node.NamedChild(index), false)
			}
			return
		}
		key := name + "\x00" + strconv.FormatUint(uint64(node.StartByte()), 10)
		if _, duplicate := seen[key]; !duplicate {
			seen[key] = struct{}{}
			result = append(result, &basev0.SemanticUse{Name: name, Location: semanticLocation(path, node)})
		}
		for index := 0; index < int(node.NamedChildCount()); index++ {
			visit(node.NamedChild(index), false)
		}
	}
	visit(declaration, true)
	sort.Slice(result, func(i, j int) bool {
		if result[i].GetLocation().GetStartLine() != result[j].GetLocation().GetStartLine() {
			return result[i].GetLocation().GetStartLine() < result[j].GetLocation().GetStartLine()
		}
		return result[i].GetName() < result[j].GetName()
	})
	return result
}

func semanticCallName(node *sitter.Node, body []byte) string {
	var target *sitter.Node
	switch node.Type() {
	case "call", "call_expression", "invocation_expression":
		target = node.ChildByFieldName("function")
		if target == nil || target.IsNull() {
			target = node.NamedChild(0)
		}
	case "method_invocation":
		name := node.ChildByFieldName("name")
		object := node.ChildByFieldName("object")
		if name == nil || name.IsNull() {
			return ""
		}
		value := strings.TrimSpace(name.Content(body))
		if object != nil && !object.IsNull() {
			value = strings.TrimSpace(object.Content(body)) + "." + value
		}
		return cleanSemanticName(value)
	default:
		return ""
	}
	if target == nil || target.IsNull() {
		return ""
	}
	return cleanSemanticName(target.Content(body))
}

func cleanSemanticName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "new ")
	var out strings.Builder
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' || r == ':' {
			out.WriteRune(r)
			continue
		}
		if out.Len() > 0 {
			break
		}
	}
	return strings.Trim(out.String(), ".:")
}

func semanticTypeNode(nodeType string) bool {
	switch nodeType {
	case "type_identifier", "generic_type", "user_type", "nullable_type", "predefined_type":
		return true
	default:
		return false
	}
}

func semanticImports(language, path string, root *sitter.Node, body []byte) []string {
	switch language {
	case "go":
		parsed, err := parser.ParseFile(token.NewFileSet(), path, body, parser.ImportsOnly)
		if err != nil {
			return nil
		}
		imports := make([]string, 0, len(parsed.Imports))
		for _, declaration := range parsed.Imports {
			if value, err := strconv.Unquote(declaration.Path.Value); err == nil {
				imports = append(imports, value)
			}
		}
		return canonicalImports(imports)
	case "python":
		return canonicalImports(extractPythonImports(root, body))
	case "typescript":
		return canonicalImports(extractTypeScriptImports(root, body))
	case "java", "kotlin":
		return canonicalImports(extractJVMImports(root, body))
	case "csharp":
		return canonicalImports(extractCSharpImports(root, body))
	default:
		return nil
	}
}
