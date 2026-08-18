package semantic

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
	osfs "io/fs"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/codefly-dev/core/code"
	"github.com/codefly-dev/core/code/semanticcontract"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	tskotlin "github.com/tree-sitter-grammars/tree-sitter-kotlin/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
	tscsharp "github.com/tree-sitter/tree-sitter-c-sharp/bindings/go"
	tsgo "github.com/tree-sitter/tree-sitter-go/bindings/go"
	tsjava "github.com/tree-sitter/tree-sitter-java/bindings/go"
	tsjavascript "github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	tspython "github.com/tree-sitter/tree-sitter-python/bindings/go"
	tsrust "github.com/tree-sitter/tree-sitter-rust/bindings/go"
	tstypescript "github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

type semanticLanguage struct {
	name       string
	grammar    *sitter.Language
	extensions map[string]struct{}
}

var semanticLanguages = []semanticLanguage{
	{name: "go", grammar: sitter.NewLanguage(tsgo.Language()), extensions: extensionSet(".go")},
	{name: "python", grammar: sitter.NewLanguage(tspython.Language()), extensions: extensionSet(".py")},
	{name: "rust", grammar: sitter.NewLanguage(tsrust.Language()), extensions: extensionSet(".rs")},
	{name: "typescript", grammar: sitter.NewLanguage(tstypescript.LanguageTypescript()), extensions: extensionSet(".ts", ".mts", ".cts")},
	{name: "typescript", grammar: sitter.NewLanguage(tstypescript.LanguageTSX()), extensions: extensionSet(".tsx")},
	{name: "javascript", grammar: sitter.NewLanguage(tsjavascript.Language()), extensions: extensionSet(".js", ".jsx", ".mjs", ".cjs")},
	{name: "java", grammar: sitter.NewLanguage(tsjava.Language()), extensions: extensionSet(".java")},
	{name: "kotlin", grammar: sitter.NewLanguage(tskotlin.Language()), extensions: extensionSet(".kt", ".kts")},
	{name: "csharp", grammar: sitter.NewLanguage(tscsharp.Language()), extensions: extensionSet(".cs")},
}

const (
	semanticFunction  = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_FUNCTION
	semanticMethod    = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_METHOD
	semanticClass     = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_CLASS
	semanticStruct    = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_STRUCT
	semanticInterface = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_INTERFACE
	semanticEnum      = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_ENUM
	semanticField     = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_FIELD
	semanticVariable  = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_VARIABLE
	semanticConstant  = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_CONSTANT
	semanticAlias     = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_TYPE_ALIAS
	semanticModule    = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_MODULE
)

var ecmaScriptDeclarationKinds = map[string]basev0.SemanticSymbolKind{
	"function_declaration":       semanticFunction,
	"method_definition":          semanticMethod,
	"class_declaration":          semanticClass,
	"abstract_class_declaration": semanticClass,
	"interface_declaration":      semanticInterface,
	"enum_declaration":           semanticEnum,
	"type_alias_declaration":     semanticAlias,
	"lexical_declaration":        semanticVariable,
	"variable_declaration":       semanticVariable,
}

var semanticDeclarationKinds = map[string]map[string]basev0.SemanticSymbolKind{
	"go":         {"function_declaration": semanticFunction, "method_declaration": semanticMethod, "type_spec": semanticAlias, "type_alias": semanticAlias, "const_spec": semanticConstant, "var_spec": semanticVariable},
	"python":     {"function_definition": semanticFunction, "class_definition": semanticClass, "assignment": semanticVariable},
	"rust":       {"function_item": semanticFunction, "function_signature_item": semanticFunction, "struct_item": semanticStruct, "union_item": semanticStruct, "enum_item": semanticEnum, "trait_item": semanticInterface, "type_item": semanticAlias, "associated_type": semanticAlias, "const_item": semanticConstant, "static_item": semanticVariable, "field_declaration": semanticField, "enum_variant": semanticConstant, "mod_item": semanticModule},
	"typescript": ecmaScriptDeclarationKinds,
	"javascript": ecmaScriptDeclarationKinds,
	"java":       {"method_declaration": semanticMethod, "constructor_declaration": semanticMethod, "class_declaration": semanticClass, "record_declaration": semanticClass, "interface_declaration": semanticInterface, "enum_declaration": semanticEnum, "field_declaration": semanticVariable},
	"kotlin":     {"function_declaration": semanticMethod, "class_declaration": semanticClass, "object_declaration": semanticClass, "property_declaration": semanticVariable, "type_alias": semanticAlias},
	"csharp":     {"method_declaration": semanticMethod, "constructor_declaration": semanticMethod, "class_declaration": semanticClass, "record_declaration": semanticClass, "interface_declaration": semanticInterface, "struct_declaration": semanticStruct, "enum_declaration": semanticEnum, "field_declaration": semanticVariable, "property_declaration": semanticVariable},
}

// SemanticIndex performs one deterministic scan through the server VFS.
// Parse failures degrade individual files while retaining valid evidence from
// the rest of the code unit. An unsupported unit is explicit, never an empty
// response that a caller could mistake for complete coverage.
func (a *Analyzer) SemanticIndex(ctx context.Context, sourceDir string, fs code.VFS) (*basev0.SemanticIndex, error) {
	index := &basev0.SemanticIndex{
		State:           basev0.SemanticIndexState_SEMANTIC_INDEX_STATE_NOT_ATTEMPTED,
		Analyzer:        "codefly-core/tree-sitter",
		AnalyzerVersion: semanticcontract.Version,
	}
	err := fs.WalkDir(sourceDir, func(filename string, entry osfs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			if filename != sourceDir && code.SkipInspectionDir(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		definition, ok := semanticLanguageForExtension(strings.ToLower(filepath.Ext(entry.Name())))
		if !ok {
			return nil
		}
		relative, err := filepath.Rel(sourceDir, filename)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		info, err := fs.Stat(filename)
		if err != nil {
			return err
		}
		if info.Size() > code.MaxSourceFileSize {
			index.Issues = append(index.Issues, semanticIssue("file_too_large", relative, fmt.Sprintf("source file exceeds %d-byte semantic inspection limit", code.MaxSourceFileSize)))
			return nil
		}
		body, err := fs.ReadFile(filename)
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
	return index, nil
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
	tree, err := parseSyntaxTree(ctx, definition.grammar, body)
	if err != nil {
		return file, nil, err
	}
	defer tree.Close()
	root := tree.RootNode()
	file.Imports = semanticImports(definition.name, path, root, body)
	if root.HasError() {
		return file, nil, fmt.Errorf("%s", treeSitterSyntaxDiagnostic(root))
	}
	packageName := semanticPackage(definition.name, path, root, body)
	var symbols []*basev0.SemanticSymbol
	projectSemanticDeclarations(definition.name, path, packageName, "", false, root, body, &symbols, nil)
	return file, symbols, nil
}

// semanticSymbolProjection is retained only inside the Codefly execution
// boundary. It joins one body-free semantic fact to the exact parser-owned
// declaration span used by typed symbol mutation.
type semanticSymbolProjection struct {
	symbol         *basev0.SemanticSymbol
	startByte      uint
	endByte        uint
	declarationKey string
}

func projectSemanticDeclarations(language, path, packageName, parent string, inCallable bool, node *sitter.Node, body []byte, out *[]*basev0.SemanticSymbol, projections *[]semanticSymbolProjection) {
	if node == nil {
		return
	}
	declarations := semanticDeclarations(language, node, body, inCallable)
	nextParent := parent
	nextInCallable := inCallable
	if language == "rust" && node.Kind() == "impl_item" {
		if receiver := rustImplReceiver(node, body); receiver != "" {
			nextParent = qualifySemanticName(packageName, parent, receiver)
		}
	}
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
		if bodyNode != nil {
			bodyBytes = nodeBytes(bodyNode, body)
		} else if semanticDataDeclaration(declaration.kind) {
			bodyBytes = nodeBytes(node, body)
		}
		declarationBytes := nodeBytes(node, body)
		symbol := &basev0.SemanticSymbol{
			Name: name, QualifiedName: qualified, Kind: declaration.kind,
			Location: semanticLocation(path, node), Package: packageName,
			ParentQualifiedName: parent, Signature: boundedSignature(signatureBytes),
			SignatureSha256: semanticHash(signatureBytes), DeclarationSha256: semanticHash(declarationBytes),
		}
		if len(bodyBytes) > 0 {
			symbol.BodySha256 = semanticHash(bodyBytes)
		}
		if semanticCallable(declaration.kind) {
			symbol.Calls = semanticUses(language, path, node, body, true)
			symbol.References = semanticUses(language, path, node, body, false)
		}
		*out = append(*out, symbol)
		if projections != nil {
			*projections = append(*projections, semanticSymbolProjection{
				symbol: symbol, startByte: node.StartByte(), endByte: node.EndByte(),
				declarationKey: fmt.Sprintf("%d:%d", node.StartByte(), node.EndByte()),
			})
		}
		if semanticContainer(declaration.kind) || semanticCallable(declaration.kind) {
			nextParent = qualified
		}
		if semanticCallable(declaration.kind) {
			nextInCallable = true
		}
	}
	for index := uint(0); index < node.NamedChildCount(); index++ {
		child := node.NamedChild(index)
		if (len(declarations) > 0 || nextParent != parent) && isNestedSemanticBody(node, child) {
			projectSemanticDeclarations(language, path, packageName, nextParent, nextInCallable, child, body, out, projections)
			continue
		}
		projectSemanticDeclarations(language, path, packageName, parent, inCallable, child, body, out, projections)
	}
}

type semanticDeclaration struct {
	name string
	kind basev0.SemanticSymbolKind
}

func semanticDeclarations(language string, node *sitter.Node, body []byte, inCallable bool) []semanticDeclaration {
	kind, ok := semanticDeclarationKind(language, node.Kind())
	if !ok {
		return nil
	}
	if semanticDataDeclaration(kind) && inCallable {
		return nil
	}
	if language == "go" && node.Kind() == "type_spec" {
		if declaredType := node.ChildByFieldName("type"); declaredType != nil {
			switch declaredType.Kind() {
			case "struct_type":
				kind = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_STRUCT
			case "interface_type":
				kind = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_INTERFACE
			}
		}
	}
	if language == "python" && node.Kind() == "function_definition" && insideSemanticContainer(language, node) {
		kind = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_METHOD
	}
	if language == "kotlin" && node.Kind() == "function_declaration" && !insideSemanticContainer(language, node) {
		kind = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_FUNCTION
	}
	if language == "rust" && (node.Kind() == "function_item" || node.Kind() == "function_signature_item") && insideSemanticContainer(language, node) {
		kind = basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_METHOD
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
	if name := node.ChildByFieldName("name"); name != nil {
		return []string{strings.TrimSpace(name.Utf8Text(body))}
	}
	if node.Kind() == "assignment" {
		if left := node.ChildByFieldName("left"); left != nil && (left.Kind() == "identifier" || left.Kind() == "attribute") {
			return []string{strings.TrimSpace(left.Utf8Text(body))}
		}
	}
	// Older tree-sitter Kotlin grammars expose declaration identifiers as an
	// unnamed direct simple_identifier rather than a named field.
	for index := uint(0); index < node.NamedChildCount(); index++ {
		candidate := node.NamedChild(index)
		if candidate.Kind() == "simple_identifier" || candidate.Kind() == "type_identifier" || candidate.Kind() == "identifier" {
			return []string{strings.TrimSpace(candidate.Utf8Text(body))}
		}
	}
	var names []string
	walkSyntax(node, func(candidate *sitter.Node) {
		if candidate == node {
			return
		}
		switch candidate.Kind() {
		case "variable_declarator", "variable_declaration", "const_spec", "var_spec":
			if name := candidate.ChildByFieldName("name"); name != nil {
				names = append(names, strings.TrimSpace(name.Utf8Text(body)))
			}
		}
	})
	return code.CanonicalImports(names)
}

func semanticBodyNode(node *sitter.Node) *sitter.Node {
	for _, field := range []string{"body", "value"} {
		if child := node.ChildByFieldName(field); child != nil {
			return child
		}
	}
	if node.Kind() == "type_spec" || node.Kind() == "type_alias" {
		if child := node.ChildByFieldName("type"); child != nil {
			return child
		}
	}
	return nil
}

func semanticSignatureBytes(node, bodyNode *sitter.Node, body []byte, kind basev0.SemanticSymbolKind, name string) []byte {
	if semanticDataDeclaration(kind) || (semanticCallable(kind) && bodyNode == nil) {
		return []byte(name)
	}
	start, end := int(node.StartByte()), int(node.EndByte())
	if bodyNode != nil {
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
	start, end := node.StartPosition(), node.EndPosition()
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
		switch node.Kind() {
		case "package_clause", "package_declaration", "package_header", "namespace_declaration", "file_scoped_namespace_declaration":
			if name := node.ChildByFieldName("name"); name != nil {
				packageName = strings.TrimSpace(name.Utf8Text(body))
				return
			}
			value := strings.TrimSpace(node.Utf8Text(body))
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
		basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_ENUM,
		basev0.SemanticSymbolKind_SEMANTIC_SYMBOL_KIND_MODULE:
		return true
	default:
		return false
	}
}

func isNestedSemanticBody(parent, child *sitter.Node) bool {
	body := semanticBodyNode(parent)
	return body != nil && child != nil && body.StartByte() == child.StartByte() && body.EndByte() == child.EndByte()
}

func insideSemanticContainer(language string, node *sitter.Node) bool {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if language == "rust" && parent.Kind() == "impl_item" {
			return true
		}
		if kind, ok := semanticDeclarationKind(language, parent.Kind()); ok && semanticContainer(kind) {
			return true
		}
	}
	return false
}

func rustImplReceiver(node *sitter.Node, body []byte) string {
	receiver := node.ChildByFieldName("type")
	if receiver == nil {
		return ""
	}
	return cleanSemanticName(receiver.Utf8Text(body))
}

func goReceiverName(node *sitter.Node, body []byte) string {
	receiver := node.ChildByFieldName("receiver")
	if receiver == nil {
		return ""
	}
	result := ""
	walkSyntax(receiver, func(candidate *sitter.Node) {
		if candidate.Kind() == "type_identifier" {
			result = strings.TrimSpace(candidate.Utf8Text(body))
		}
	})
	return result
}

func semanticUses(language, path string, declaration *sitter.Node, body []byte, calls bool) []*basev0.SemanticUse {
	seen := make(map[string]struct{})
	var result []*basev0.SemanticUse
	var visit func(*sitter.Node, bool)
	visit = func(node *sitter.Node, root bool) {
		if node == nil {
			return
		}
		if !root {
			if _, nested := semanticDeclarationKind(language, node.Kind()); nested {
				return
			}
		}
		var name string
		if calls {
			name = semanticCallName(node, body)
		} else if semanticTypeNode(node.Kind()) {
			name = cleanSemanticName(node.Utf8Text(body))
		}
		if name == "" {
			for index := uint(0); index < node.NamedChildCount(); index++ {
				visit(node.NamedChild(index), false)
			}
			return
		}
		key := name + "\x00" + strconv.FormatUint(uint64(node.StartByte()), 10)
		if _, duplicate := seen[key]; !duplicate {
			seen[key] = struct{}{}
			result = append(result, &basev0.SemanticUse{Name: name, Location: semanticLocation(path, node)})
		}
		for index := uint(0); index < node.NamedChildCount(); index++ {
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
	switch node.Kind() {
	case "call", "call_expression", "invocation_expression":
		target = node.ChildByFieldName("function")
		if target == nil {
			target = node.NamedChild(0)
		}
	case "method_invocation":
		name := node.ChildByFieldName("name")
		object := node.ChildByFieldName("object")
		if name == nil {
			return ""
		}
		value := strings.TrimSpace(name.Utf8Text(body))
		if object != nil {
			value = strings.TrimSpace(object.Utf8Text(body)) + "." + value
		}
		return cleanSemanticName(value)
	default:
		return ""
	}
	if target == nil {
		return ""
	}
	return cleanSemanticName(target.Utf8Text(body))
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
	case "type_identifier", "scoped_type_identifier", "generic_type", "user_type", "nullable_type", "predefined_type", "primitive_type":
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
		return code.CanonicalImports(imports)
	case "python":
		return code.CanonicalImports(extractPythonImports(root, body))
	case "rust":
		return code.CanonicalImports(extractRustImports(root, body))
	case "typescript", "javascript":
		return code.CanonicalImports(extractTypeScriptImports(root, body))
	case "java", "kotlin":
		return code.CanonicalImports(extractJVMImports(root, body))
	case "csharp":
		return code.CanonicalImports(extractCSharpImports(root, body))
	default:
		return nil
	}
}

func extractRustImports(root *sitter.Node, body []byte) []string {
	var imports []string
	walkSyntax(root, func(node *sitter.Node) {
		var keyword string
		switch node.Kind() {
		case "use_declaration":
			keyword = "use "
		case "extern_crate_declaration":
			keyword = "extern crate "
		default:
			return
		}
		value := strings.TrimSpace(node.Utf8Text(body))
		position := strings.Index(value, keyword)
		if position < 0 {
			return
		}
		value = strings.TrimSpace(strings.TrimSuffix(value[position+len(keyword):], ";"))
		if value != "" {
			imports = append(imports, value)
		}
	})
	return imports
}
