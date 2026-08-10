package code

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

func TestApplySymbolPatchUsesAnalyzerOwnedDeclarationAcrossSupportedLanguages(t *testing.T) {
	tests := []struct {
		name, file, before, qualifiedName, declaration, replacement string
	}{
		{name: "go", file: "main.go", before: "package api\n\nfunc Value() int { return 1 }\n", qualifiedName: "api.Value", declaration: "func Value() int { return 1 }", replacement: "func Value() int { return 2 }"},
		{name: "python", file: "app.py", before: "def value():\n    return 1\n", qualifiedName: "app.value", declaration: "def value():\n    return 1", replacement: "def value():\n    return 2"},
		{name: "typescript", file: "service.ts", before: "export class Service { value(): number { return 1; } }\n", qualifiedName: "service.Service.value", declaration: "value(): number { return 1; }", replacement: "value(): number { return 2; }"},
		{name: "java", file: "Worker.java", before: "package demo; class Worker { int value() { return 1; } }\n", qualifiedName: "demo.Worker.value", declaration: "int value() { return 1; }", replacement: "int value() { return 2; }"},
		{name: "kotlin", file: "Queue.kt", before: "package demo\nfun value(): Int = 1\n", qualifiedName: "demo.value", declaration: "fun value(): Int = 1", replacement: "fun value(): Int = 2"},
		{name: "csharp", file: "Cart.cs", before: "namespace Shop.Cart; public class Cart { public int Value() { return 1; } }\n", qualifiedName: "Shop.Cart.Cart.Value", declaration: "public int Value() { return 1; }", replacement: "public int Value() { return 2; }"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, test.file)
			if err := os.WriteFile(path, []byte(test.before), 0o644); err != nil {
				t.Fatal(err)
			}
			server := NewDefaultCodeServer(root)
			t.Cleanup(func() { _ = server.Close() })
			request := &codev0.ApplySymbolPatchRequest{
				File: test.file, QualifiedName: test.qualifiedName,
				ExpectedDeclarationSha256: semanticHash([]byte(test.declaration)),
				NewSource:                 test.replacement, FixMode: basev0.FixMode_FIX_MODE_NONE, DryRun: true,
			}
			preview, err := server.Execute(t.Context(), &codev0.CodeRequest{Operation: &codev0.CodeRequest_ApplySymbolPatch{ApplySymbolPatch: request}})
			if err != nil || !preview.GetApplySymbolPatch().GetSuccess() || !preview.GetApplySymbolPatch().GetChanged() || preview.GetApplySymbolPatch().GetWrote() {
				t.Fatalf("preview response=%+v failure=%+v err=%v", preview.GetApplySymbolPatch(), preview.GetFailure(), err)
			}
			unchanged, err := os.ReadFile(path)
			if err != nil || string(unchanged) != test.before {
				t.Fatalf("dry run changed file: content=%q err=%v", unchanged, err)
			}
			request.DryRun = false
			applied, err := server.Execute(t.Context(), &codev0.CodeRequest{Operation: &codev0.CodeRequest_ApplySymbolPatch{ApplySymbolPatch: request}})
			if err != nil || !applied.GetApplySymbolPatch().GetSuccess() || !applied.GetApplySymbolPatch().GetWrote() {
				t.Fatalf("apply response=%+v failure=%+v err=%v", applied.GetApplySymbolPatch(), applied.GetFailure(), err)
			}
			current, err := os.ReadFile(path)
			if err != nil || !strings.Contains(string(current), test.replacement) || strings.Contains(string(current), test.declaration) {
				t.Fatalf("applied file=%q err=%v", current, err)
			}
		})
	}
}

func TestApplySymbolPatchFailsClosedOnStaleAnchorAndInvalidReplacement(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "main.go")
	before := "package api\n\nfunc Value() int { return 1 }\n"
	if err := os.WriteFile(path, []byte(before), 0o644); err != nil {
		t.Fatal(err)
	}
	server := NewDefaultCodeServer(root)
	t.Cleanup(func() { _ = server.Close() })
	request := func(hash, replacement string) *codev0.CodeRequest {
		return &codev0.CodeRequest{Operation: &codev0.CodeRequest_ApplySymbolPatch{ApplySymbolPatch: &codev0.ApplySymbolPatchRequest{
			File: "main.go", QualifiedName: "api.Value", ExpectedDeclarationSha256: hash,
			NewSource: replacement, FixMode: basev0.FixMode_FIX_MODE_NONE,
		}}}
	}
	stale, err := server.Execute(t.Context(), request(strings.Repeat("0", 64), "func Value() int { return 2 }"))
	if err != nil || stale.GetFailure().GetCode() != basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED || stale.GetApplySymbolPatch().GetFailureReason() != basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_STALE_ANCHOR || stale.GetApplySymbolPatch().GetDeclarationSha256() != semanticHash([]byte("func Value() int { return 1 }")) {
		t.Fatalf("stale response=%+v failure=%+v err=%v", stale.GetApplySymbolPatch(), stale.GetFailure(), err)
	}
	invalid, err := server.Execute(t.Context(), request(semanticHash([]byte("func Value() int { return 1 }")), "func Value("))
	if err != nil || invalid.GetFailure().GetCode() != basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED || invalid.GetApplySymbolPatch().GetFailureReason() != basev0.SymbolPatchFailureReason_SYMBOL_PATCH_FAILURE_REASON_INVALID_REPLACEMENT {
		t.Fatalf("invalid response=%+v failure=%+v err=%v", invalid.GetApplySymbolPatch(), invalid.GetFailure(), err)
	}
	current, err := os.ReadFile(path)
	if err != nil || string(current) != before {
		t.Fatalf("rejected patches changed file: content=%q err=%v", current, err)
	}
}
