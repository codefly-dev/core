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
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
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
	if s.semantic == nil {
		result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_UNSUPPORTED_LANGUAGE
		return failure(basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION, "code.apply-symbol-patch", "semantic symbol mutation requires a source-semantics analyzer")
	}
	language, ok := s.semantic.Language(file)
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
	projections, err := s.semantic.DeclarationSpans(ctx, file, before)
	if err != nil {
		return failure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.apply-symbol-patch", fmt.Sprintf("inspect current declaration: %v", err))
	}
	qualified := make([]SymbolSpan, 0, len(projections))
	for _, projection := range projections {
		if projection.Symbol.GetQualifiedName() == qualifiedName {
			qualified = append(qualified, projection)
		}
	}
	if len(qualified) == 0 {
		result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_NOT_FOUND
		return failure(basev0.FailureCode_FAILURE_CODE_NOT_FOUND, "code.apply-symbol-patch", fmt.Sprintf("qualified symbol %q was not found in %s", qualifiedName, file))
	}
	anchored := make([]SymbolSpan, 0, len(qualified))
	for _, projection := range qualified {
		if projection.Symbol.GetDeclarationSha256() == expected {
			anchored = append(anchored, projection)
		}
	}
	if len(anchored) == 0 {
		result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_STALE_ANCHOR
		if len(qualified) == 1 {
			result.DeclarationSha256 = qualified[0].Symbol.GetDeclarationSha256()
		}
		return failure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.apply-symbol-patch", fmt.Sprintf("declaration hash for %q is stale", qualifiedName))
	}
	if len(anchored) != 1 {
		result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_AMBIGUOUS
		return failure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.apply-symbol-patch", fmt.Sprintf("qualified symbol %q and declaration hash are ambiguous", qualifiedName))
	}
	target := anchored[0]
	for _, projection := range projections {
		if projection.DeclarationKey == target.DeclarationKey && projection.Symbol.GetQualifiedName() != target.Symbol.GetQualifiedName() {
			result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_SHARED_DECLARATION
			return failure(basev0.FailureCode_FAILURE_CODE_UNSUPPORTED_OPERATION, "code.apply-symbol-patch", fmt.Sprintf("%q shares one declaration with another symbol; use a typed multi-symbol mutation", qualifiedName))
		}
	}
	start, end := int(target.StartByte), int(target.EndByte)
	if start < 0 || end < start || end > len(before) {
		return failure(basev0.FailureCode_FAILURE_CODE_INTERNAL, "code.apply-symbol-patch", "semantic analyzer returned an invalid declaration span")
	}
	// Tree-sitter declaration spans begin at the first declaration token, not
	// at the line's outer indentation. Accept both analyzer-relative source and
	// the file-shaped source a model naturally copies from a read: when the
	// replacement already carries the exact existing prefix, replace that
	// prefix too instead of silently doubling it.
	replacementStart := start
	if lineStart := bytes.LastIndexByte(before[:start], '\n') + 1; lineStart < start {
		prefix := before[lineStart:start]
		if len(bytes.Trim(prefix, " \t")) == 0 && strings.HasPrefix(req.GetNewSource(), string(prefix)) {
			replacementStart = lineStart
		}
	}
	after := make([]byte, 0, len(before)-(end-replacementStart)+len(req.GetNewSource()))
	after = append(after, before[:replacementStart]...)
	after = append(after, req.GetNewSource()...)
	after = append(after, before[end:]...)
	if err := s.semantic.ValidateSyntax(ctx, file, after); err != nil {
		result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_INVALID_REPLACEMENT
		return failure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.apply-symbol-patch", fmt.Sprintf("replacement is not valid %s source: %v", language, err))
	}
	updated, err := s.semantic.DeclarationSpans(ctx, file, after)
	if err != nil {
		result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_INVALID_REPLACEMENT
		return failure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.apply-symbol-patch", fmt.Sprintf("inspect replacement declaration: %v", err))
	}
	preservedIdentity := 0
	for _, projection := range updated {
		if projection.Symbol.GetQualifiedName() == qualifiedName && projection.StartByte == target.StartByte {
			preservedIdentity++
		}
	}
	if preservedIdentity != 1 {
		result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_INVALID_REPLACEMENT
		return failure(basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED, "code.apply-symbol-patch", fmt.Sprintf("replacement must preserve exactly one qualified symbol %q", qualifiedName))
	}
	var actions []string
	var output string
	if req.GetFixMode() != basev0.FixMode_FIX_MODE_NONE && s.sourceFixer != nil {
		fixed, fixErr := s.sourceFixer(ctx, FixInput{Path: file, Content: after, Mode: req.GetFixMode()})
		if fixErr != nil {
			return failure(basev0.FailureCode_FAILURE_CODE_PROCESS_FAILED, "code.apply-symbol-patch.fix", fixErr.Error())
		}
		if err := s.semantic.ValidateSyntax(ctx, file, fixed.Content); err != nil {
			result.FailureReason = basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_INVALID_REPLACEMENT
			return failure(basev0.FailureCode_FAILURE_CODE_PROCESS_FAILED, "code.apply-symbol-patch.fix", fmt.Sprintf("fixer returned invalid %s source: %v", language, err))
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

func canonicalSemanticSHA256(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32
}
