package proto

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestNewBufTracksEveryGenerationInput(t *testing.T) {
	generator, err := NewBuf(context.Background(), t.TempDir())
	if err != nil {
		t.Fatalf("NewBuf: %v", err)
	}
	dependency := generator.dependencies.Components[0]
	for _, path := range []string{
		"proto/api.proto",
		"proto/buf.gen.yaml",
		"proto/buf.yaml",
		"proto/buf.lock",
	} {
		if !dependency.Keep(path) {
			t.Errorf("generation input %q is not tracked", path)
		}
	}
	if dependency.Keep("code/pkg/gen/api.pb.go") {
		t.Fatal("generated output must not invalidate its own input cache")
	}
}

func TestBufCleanGeneratedDirsIsStrictlyScoped(t *testing.T) {
	root := t.TempDir()
	generated := filepath.Join(root, "code", "pkg", "gen")
	if err := os.MkdirAll(generated, 0o755); err != nil {
		t.Fatalf("mkdir generated directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(generated, "stale.pb.go"), []byte("stale"), 0o644); err != nil {
		t.Fatalf("write generated file: %v", err)
	}

	generator, err := NewBuf(context.Background(), root)
	if err != nil {
		t.Fatalf("NewBuf: %v", err)
	}
	generator.WithGeneratedDirs(generated)
	if err := generator.cleanGeneratedDirs(); err != nil {
		t.Fatalf("cleanGeneratedDirs: %v", err)
	}
	if _, err := os.Stat(generated); !os.IsNotExist(err) {
		t.Fatalf("generated directory still exists or stat failed unexpectedly: %v", err)
	}

	outside := t.TempDir()
	generator.generatedDirs = []string{outside}
	if err := generator.cleanGeneratedDirs(); err == nil {
		t.Fatal("cleanGeneratedDirs accepted an output outside the generator root")
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside directory was touched: %v", err)
	}
}
