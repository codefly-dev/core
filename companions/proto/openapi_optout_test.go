package proto

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestWithoutOpenAPILeavesFlagUnsetByDefault locks in that the OpenAPI
// post-generation stage remains opt-out: a freshly constructed Buf runs it, and
// WithoutOpenAPI is the only thing that suppresses it. The builder is fluent, so
// it must return the same receiver for chaining alongside the other With* calls.
func TestWithoutOpenAPILeavesFlagUnsetByDefault(t *testing.T) {
	generator, err := NewBuf(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("NewBuf: %v", err)
	}
	if generator.skipOpenAPI {
		t.Fatal("NewBuf must run the OpenAPI stage by default (skipOpenAPI = true)")
	}
	if got := generator.WithoutOpenAPI(); got != generator {
		t.Fatal("WithoutOpenAPI must return the receiver for fluent chaining")
	}
	if !generator.skipOpenAPI {
		t.Fatal("WithoutOpenAPI must opt out of the OpenAPI stage (skipOpenAPI = false)")
	}
}

// TestEmitOpenAPIArtifactsOptOutLeavesSwaggerUntouched is the behavioral guard
// for the downstream consumer: service-python-fastapi generates gRPC stubs from
// its service root, which also holds a FastAPI openapi/ REST contract. Without
// the opt-out, every Sync would run npx openapi-typescript and drop a stray .ts
// into the Python service. With WithoutOpenAPI the stage short-circuits before
// touching the swagger inputs or spawning any process — so a nil runner is
// safe here precisely because it is never reached.
func TestEmitOpenAPIArtifactsOptOutLeavesSwaggerUntouched(t *testing.T) {
	root := t.TempDir()
	openapiDir := filepath.Join(root, "openapi")
	if err := os.MkdirAll(openapiDir, 0o755); err != nil {
		t.Fatalf("mkdir openapi directory: %v", err)
	}

	const canonicalDoc = `{"swagger":"2.0","info":{"title":"api"}}`
	const extraDoc = `{"swagger":"2.0","info":{"title":"extra"}}`
	canonical := filepath.Join(openapiDir, "api.swagger.json")
	extra := filepath.Join(openapiDir, "extra.swagger.json")
	if err := os.WriteFile(canonical, []byte(canonicalDoc), 0o644); err != nil {
		t.Fatalf("write canonical swagger: %v", err)
	}
	if err := os.WriteFile(extra, []byte(extraDoc), 0o644); err != nil {
		t.Fatalf("write extra swagger: %v", err)
	}

	generator, err := NewBuf(context.Background(), root)
	if err != nil {
		t.Fatalf("NewBuf: %v", err)
	}
	generator.WithoutOpenAPI()

	// A nil runner would panic the moment the TypeScript loop tried to spawn a
	// process; reaching this without a panic proves the stage short-circuited.
	if err := generator.emitOpenAPIArtifacts(context.Background(), nil); err != nil {
		t.Fatalf("emitOpenAPIArtifacts with opt-out: %v", err)
	}

	for path, want := range map[string]string{canonical: canonicalDoc, extra: extraDoc} {
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("swagger input %q was disturbed: %v", path, err)
		}
		if string(got) != want {
			t.Fatalf("swagger input %q content = %q, want %q", path, got, want)
		}
	}

	entries, err := os.ReadDir(openapiDir)
	if err != nil {
		t.Fatalf("read openapi directory: %v", err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".ts" {
			t.Fatalf("opt-out still produced a TypeScript file: %s", entry.Name())
		}
		if filepath.Ext(entry.Name()) == ".json" && entry.Name() != "api.swagger.json" && entry.Name() != "extra.swagger.json" {
			t.Fatalf("opt-out produced an unexpected intermediate file: %s", entry.Name())
		}
	}
}
