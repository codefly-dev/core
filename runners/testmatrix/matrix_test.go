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
// the matrix harness. It runs `echo hello` under each available backend
// and asserts the output contains "hello". Backends that aren't available
// on the host are skipped by the harness — so on a machine with just
// native + docker, you see PASS (native), SKIP (nix), PASS (docker).
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
	})
}
