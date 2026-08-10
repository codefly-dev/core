package code

// ARCHITECTURE: typed symbol mutation lives beside semantic projection inside
// Codefly. The same parser owns declaration identity, hash preconditions,
// byte ranges, syntax validation, and the final VFS write. Orchestration
// callers never reconstruct a symbol boundary from project bytes or lines.

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	sitter "github.com/smacker/go-tree-sitter"
)

const semanticSymbolPatchStrategy = "semantic-qualified-declaration"

// applySymbolPatch replaces one complete declaration selected by the exact
// qualified_name emitted by GetSemanticIndex and an exact declaration hash.
func (s *DefaultCodeServer) applySymbolPatch(ctx context.Context, req *codev0.ApplySymbolPatchRequest) (*codev0.CodeResponse, error) {
	result := &codev0.ApplySymbolPatchResponse{Success: false}
	failure := func(code basev0.FailureCode, operation, message string) (*codev0.CodeResponse, error) {
		return codeFailure(&codev0.CodeResponse{Result: &codev0.CodeResponse_ApplySymbolPatch{ApplySymbolPatch: result}}, code, operation, message), nil
	}
	if req == nil {
		return failure(basev0.FailureCode_FAILURE_CODE_INVALID_ARGUMENT, "code.apply-symbol-patch", "request is required")
	}
	file := strings.TrimSpace(req.GetFile())
	qualifiedName := strings.TrimSpace(req.GetQualifiedName())
	expected := strings.TrimSpace(req.GetExpectedDeclarationSha256())
	if file == "" || file != req.GetFile() || qualifiedName == "" || qualifiedName != req.GetQualifiedName() {
		return failure(basev0.FailureCode_FAILURE_CODE_INVALID_ARGUMENT, "code.apply-symbol-patch", "file and qualified_name are required and must be canonical")
	}
	if !canonicalSemanticSHA256(expected) || expected != req.GetExpectedDeclarationSha256() {
		return failure(basev0.FailureCode_FAILURE_CODE_INVALID_ARGUMENT, "code.apply-symbol-patch", "expected_declaration_sha256 must be lowercase SHA-256")
	}
	definition, ok := semanticLanguageForExtension(strings.ToLower(filepath.Ext(file)))
	if !ok {
		result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_UNSUPPORTED_LANGUAGE
		return failure(basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION, "code.apply-symbol-patch", fmt.Sprintf("semantic symbol mutation is unsupported for %s", file))
	}
	absPath, err := resolvePath(s.SourceDir, file)
	if err != nil {
		return nil, err
	}
	before, err := s.FS.ReadFile(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_NOT_FOUND
			return failure(basev0.FailureCode_FAILURE_CODE_NOT_FOUND, "code.apply-symbol-patch", fmt.Sprintf("file not found: %s", file))
		}
		return nil, fmt.Errorf("reading %s: %w", file, err)
	}
	projections, err := semanticDeclarationSpans(ctx, definition, file, before)
	if err != nil {
		return failure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.apply-symbol-patch", fmt.Sprintf("inspect current declaration: %v", err))
	}
	qualified := make([]semanticSymbolProjection, 0, len(projections))
	for _, projection := range projections {
		if projection.symbol.GetQualifiedName() == qualifiedName {
			qualified = append(qualified, projection)
		}
	}
	if len(qualified) == 0 {
		result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_NOT_FOUND
		return failure(basev0.FailureCode_FAILURE_CODE_NOT_FOUND, "code.apply-symbol-patch", fmt.Sprintf("qualified symbol %q was not found in %s", qualifiedName, file))
	}
	anchored := make([]semanticSymbolProjection, 0, len(qualified))
	for _, projection := range qualified {
		if projection.symbol.GetDeclarationSha256() == expected {
			anchored = append(anchored, projection)
		}
	}
	if len(anchored) == 0 {
		result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_STALE_ANCHOR
		if len(qualified) == 1 {
			result.DeclarationSha256 = qualified[0].symbol.GetDeclarationSha256()
		}
		return failure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.apply-symbol-patch", fmt.Sprintf("declaration hash for %q is stale", qualifiedName))
	}
	if len(anchored) != 1 {
		result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_AMBIGUOUS
		return failure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.apply-symbol-patch", fmt.Sprintf("qualified symbol %q and declaration hash are ambiguous", qualifiedName))
	}
	target := anchored[0]
	for _, projection := range projections {
		if projection.declarationKey == target.declarationKey && projection.symbol.GetQualifiedName() != target.symbol.GetQualifiedName() {
			result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_SHARED_DECLARATION
			return failure(basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION, "code.apply-symbol-patch", fmt.Sprintf("%q shares one declaration with another symbol; use a typed multi-symbol mutation", qualifiedName))
		}
	}
	start, end := int(target.startByte), int(target.endByte)
	if start < 0 || end < start || end > len(before) {
		return failure(basev0.FailureCode_FAILURE_CODE_INTERNAL, "code.apply-symbol-patch", "semantic analyzer returned an invalid declaration span")
	}
	after := make([]byte, 0, len(before)-(end-start)+len(req.GetNewSource()))
	after = append(after, before[:start]...)
	after = append(after, req.GetNewSource()...)
	after = append(after, before[end:]...)
	if err := validateSemanticSyntax(ctx, definition, after); err != nil {
		result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_INVALID_REPLACEMENT
		return failure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.apply-symbol-patch", fmt.Sprintf("replacement is not valid %s source: %v", definition.name, err))
	}
	var actions []string
	var output string
	if req.GetFixMode() != basev0.FixMode_FIX_MODE_NONE && s.sourceFixer != nil {
		fixed, fixErr := s.sourceFixer(ctx, FixInput{Path: file, Content: after, Mode: req.GetFixMode()})
		if fixErr != nil {
			return failure(basev0.FailureCode_FAILURE_CODE_PROCESS_FAILED, "code.apply-symbol-patch.fix", fixErr.Error())
		}
		if err := validateSemanticSyntax(ctx, definition, fixed.Content); err != nil {
			result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_INVALID_REPLACEMENT
			return failure(basev0.FailureCode_FAILURE_CODE_PROCESS_FAILED, "code.apply-symbol-patch.fix", fmt.Sprintf("fixer returned invalid %s source: %v", definition.name, err))
		}
		after, actions, output = fixed.Content, fixed.Actions, boundedFixOutput(fixed.Output)
	}
	changed := !bytes.Equal(before, after)
	wrote := false
	if !req.GetDryRun() && changed {
		if err := s.FS.WriteFile(absPath, after, 0o644); err != nil {
			return failure(basev0.FailureCode_FAILURE_CODE_IO_FAILED, "code.apply-symbol-patch", fmt.Sprintf("write: %v", err))
		}
		wrote = true
		s.notifyWrite(ctx, "write", file, "", after)
	}
	result.Success = true
	result.Content = string(after)
	result.Strategy = semanticSymbolPatchStrategy
	result.FixActions = actions
	result.Changed = changed
	result.BeforeSha256 = sourceDigest(before)
	result.AfterSha256 = sourceDigest(after)
	result.BeforeSizeBytes = uint64(len(before))
	result.AfterSizeBytes = uint64(len(after))
	result.DeclarationSha256 = expected
	result.Wrote = wrote
	result.Output = output
	return &codev0.CodeResponse{Result: &codev0.CodeResponse_ApplySymbolPatch{ApplySymbolPatch: result}}, nil
}

func semanticDeclarationSpans(ctx context.Context, definition semanticLanguage, path string, body []byte) ([]semanticSymbolProjection, error) {
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(definition.grammar)
	tree, err := parser.ParseCtx(ctx, nil, body)
	if err != nil {
		return nil, err
	}
	defer tree.Close()
	root := tree.RootNode()
	if root.HasError() {
		return nil, fmt.Errorf("syntax tree contains errors")
	}
	packageName := semanticPackage(definition.name, path, root, body)
	var symbols []*basev0.SemanticSymbol
	var projections []semanticSymbolProjection
	projectSemanticDeclarations(definition.name, path, packageName, "", false, root, body, &symbols, &projections)
	return projections, nil
}

func validateSemanticSyntax(ctx context.Context, definition semanticLanguage, body []byte) error {
	parser := sitter.NewParser()
	defer parser.Close()
	parser.SetLanguage(definition.grammar)
	tree, err := parser.ParseCtx(ctx, nil, body)
	if err != nil {
		return err
	}
	defer tree.Close()
	if tree.RootNode().HasError() {
		return fmt.Errorf("syntax tree contains errors")
	}
	return nil
}

func canonicalSemanticSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
