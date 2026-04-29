package testmatrix_test

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	runners "github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/runners/testmatrix"
)

// TestForEachEnvironment_Echo is the canonical example + smoke test for
// the matrix harness. It runs `echo hello` under native and docker
// backends and asserts the output contains "hello".
//
// Nix is excluded via Only(...) because the harness's nix factory
// requires a flake.nix in the workspace dir, and authoring one
// inline here would either embed a real nixpkgs reference (slow,
// network-dependent) or a no-op flake that doesn't actually
// exercise anything different from native. The dedicated nix
// runner tests in runners/base and runners/golang cover the nix
// integration end-to-end with proper testdata flakes.
func TestForEachEnvironment_Echo(t *testing.T) {
	dir, err := os.MkdirTemp("", "testmatrix-*")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	defer os.RemoveAll(dir)

	testmatrix.ForEachEnvironment(t, dir, func(t *testing.T, env runners.RunnerEnvironment) {
		proc, err := env.NewProcess("echo", "hello")
		if err != nil {
			t.Fatalf("NewProcess: %v", err)
		}
		var buf bytes.Buffer
		proc.WithOutput(&buf)
		if err := proc.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if !strings.Contains(buf.String(), "hello") {
			t.Fatalf("expected output to contain %q, got %q", "hello", buf.String())
		}
	}, testmatrix.Only("native", "docker"))
}
