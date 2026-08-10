package base

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestNativeProcNaturalExitRemovesProcessGroupRecord(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	env, err := NewNativeEnvironment(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := env.Init(ctx); err != nil {
		t.Fatal(err)
	}
	proc, err := env.NewProcess("sh", "-c", "exit 0")
	if err != nil {
		t.Fatal(err)
	}
	native := proc.(*NativeProc)
	if err := proc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	pid := native.exec.Process.Pid
	path := filepath.Join(os.Getenv("HOME"), ".codefly", pgidDirName, fmt.Sprintf("%d.pgid", pid))
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("natural exit left process-group record %s: %v", path, err)
	}
}

func TestNixProcNaturalExitRemovesProcessGroupRecord(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ctx := context.Background()
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	env := &NixEnvironment{
		dir:          t.TempDir(),
		materialized: map[string]string{"PATH": filepath.Dir(shell)},
	}
	proc, err := env.NewProcess("sh", "-c", "exit 0")
	if err != nil {
		t.Fatal(err)
	}
	nix := proc.(*NixProc)
	if err := proc.Run(ctx); err != nil {
		t.Fatal(err)
	}
	pid := nix.exec.Process.Pid
	path := filepath.Join(os.Getenv("HOME"), ".codefly", pgidDirName, fmt.Sprintf("%d.pgid", pid))
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("natural exit left process-group record %s: %v", path, err)
	}
}
