package code

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
)

// newShellExecServer returns a DefaultCodeServer rooted at a fresh temp
// directory with a recognizable sentinel file. Tests use this as their
// sandbox.
func newShellExecServer(t *testing.T) (*DefaultCodeServer, string) {
	t.Helper()
	dir := t.TempDir()
	sentinel := filepath.Join(dir, "marker.txt")
	if err := os.WriteFile(sentinel, []byte("sentinel\n"), 0o644); err != nil {
		t.Fatalf("write sentinel: %v", err)
	}
	return NewDefaultCodeServer(dir), dir
}

// extractShellExec pulls the ShellExecResponse out of a CodeResponse
// envelope and fails the test loudly if it's missing.
func extractShellExec(t *testing.T, resp *codev0.CodeResponse) *codev0.ShellExecResponse {
	t.Helper()
	if resp == nil {
		t.Fatal("nil CodeResponse")
	}
	r := resp.GetShellExec()
	if r == nil {
		t.Fatalf("CodeResponse missing ShellExec result: %+v", resp)
	}
	return r
}

// ──────────────────────────────────────────────────────────
// Baseline: happy-path execution
// ──────────────────────────────────────────────────────────

func TestShellExec_EchoHello(t *testing.T) {
	s, _ := newShellExecServer(t)
	ctx := context.Background()

	resp, err := s.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ShellExec{
			ShellExec: &codev0.ShellExecRequest{Command: "echo hello"},
		},
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	r := extractShellExec(t, resp)

	if r.ExitCode != 0 {
		t.Errorf("exit code: got %d, want 0", r.ExitCode)
	}
	if !strings.Contains(r.Stdout, "hello") {
		t.Errorf("stdout missing 'hello': %q", r.Stdout)
	}
	if r.TimedOut {
		t.Errorf("unexpected TimedOut=true")
	}
	if r.Error != "" {
		t.Errorf("unexpected Error: %q", r.Error)
	}
}

// ──────────────────────────────────────────────────────────
// Rooted-at-sourceDir: commands see the workspace
// ──────────────────────────────────────────────────────────

func TestShellExec_CwdIsSourceDir(t *testing.T) {
	s, dir := newShellExecServer(t)
	ctx := context.Background()

	resp, _ := s.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ShellExec{
			ShellExec: &codev0.ShellExecRequest{Command: "cat marker.txt"},
		},
	})
	r := extractShellExec(t, resp)

	if r.ExitCode != 0 {
		t.Fatalf("cat marker.txt exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "sentinel") {
		t.Errorf("expected sentinel contents; got stdout=%q (dir=%s)", r.Stdout, dir)
	}
}

// ──────────────────────────────────────────────────────────
// Work dir override (relative to source dir)
// ──────────────────────────────────────────────────────────

func TestShellExec_WorkDir_Subdir(t *testing.T) {
	s, dir := newShellExecServer(t)
	subdir := filepath.Join(dir, "pkg", "sub")
	if err := os.MkdirAll(subdir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(subdir, "inner.txt"), []byte("inside\n"), 0o644)
	ctx := context.Background()

	resp, _ := s.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ShellExec{
			ShellExec: &codev0.ShellExecRequest{
				Command: "cat inner.txt",
				WorkDir: "pkg/sub",
			},
		},
	})
	r := extractShellExec(t, resp)

	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "inside") {
		t.Errorf("expected 'inside' in stdout, got %q", r.Stdout)
	}
}

// ──────────────────────────────────────────────────────────
// Work dir safety: absolute and traversal rejected
// ──────────────────────────────────────────────────────────

func TestShellExec_WorkDir_AbsoluteRejected(t *testing.T) {
	s, _ := newShellExecServer(t)
	ctx := context.Background()

	resp, _ := s.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ShellExec{
			ShellExec: &codev0.ShellExecRequest{
				Command: "echo hi",
				WorkDir: "/etc",
			},
		},
	})
	r := extractShellExec(t, resp)

	if r.ExitCode != -1 {
		t.Errorf("expected exit -1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Error, "absolute work_dir not allowed") {
		t.Errorf("expected absolute-path error, got %q", r.Error)
	}
}

func TestShellExec_WorkDir_TraversalRejected(t *testing.T) {
	s, _ := newShellExecServer(t)
	ctx := context.Background()

	resp, _ := s.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ShellExec{
			ShellExec: &codev0.ShellExecRequest{
				Command: "echo hi",
				WorkDir: "../../..",
			},
		},
	})
	r := extractShellExec(t, resp)

	if r.ExitCode != -1 {
		t.Errorf("expected exit -1, got %d", r.ExitCode)
	}
	if !strings.Contains(r.Error, "escapes source root") {
		t.Errorf("expected traversal error, got %q", r.Error)
	}
}

// ──────────────────────────────────────────────────────────
// Args mode (no shell interpretation)
// ──────────────────────────────────────────────────────────

func TestShellExec_ArgsMode(t *testing.T) {
	s, _ := newShellExecServer(t)
	ctx := context.Background()

	// Args mode should pass the literal string — no glob expansion,
	// no variable substitution.
	resp, _ := s.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ShellExec{
			ShellExec: &codev0.ShellExecRequest{
				Args: []string{"echo", "literal *", "$HOME"},
			},
		},
	})
	r := extractShellExec(t, resp)

	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "literal *") {
		t.Errorf("star should NOT be glob-expanded; got %q", r.Stdout)
	}
	if !strings.Contains(r.Stdout, "$HOME") {
		t.Errorf("$HOME should NOT be expanded; got %q", r.Stdout)
	}
}

// ──────────────────────────────────────────────────────────
// Non-zero exit preserved
// ──────────────────────────────────────────────────────────

func TestShellExec_NonZeroExitCode(t *testing.T) {
	s, _ := newShellExecServer(t)
	ctx := context.Background()

	resp, _ := s.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ShellExec{
			ShellExec: &codev0.ShellExecRequest{Command: "exit 42"},
		},
	})
	r := extractShellExec(t, resp)

	if r.ExitCode != 42 {
		t.Errorf("exit code: got %d, want 42", r.ExitCode)
	}
	if r.TimedOut {
		t.Errorf("unexpected TimedOut=true")
	}
}

// ──────────────────────────────────────────────────────────
// Stderr captured separately from stdout
// ──────────────────────────────────────────────────────────

func TestShellExec_StderrSeparate(t *testing.T) {
	s, _ := newShellExecServer(t)
	ctx := context.Background()

	resp, _ := s.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ShellExec{
			ShellExec: &codev0.ShellExecRequest{
				Command: "echo to-out; echo to-err >&2",
			},
		},
	})
	r := extractShellExec(t, resp)

	if !strings.Contains(r.Stdout, "to-out") {
		t.Errorf("stdout missing to-out: %q", r.Stdout)
	}
	if strings.Contains(r.Stdout, "to-err") {
		t.Errorf("stdout should not have to-err: %q", r.Stdout)
	}
	if !strings.Contains(r.Stderr, "to-err") {
		t.Errorf("stderr missing to-err: %q", r.Stderr)
	}
}

// ──────────────────────────────────────────────────────────
// Timeout triggers process-group kill
// ──────────────────────────────────────────────────────────

func TestShellExec_Timeout(t *testing.T) {
	s, _ := newShellExecServer(t)
	ctx := context.Background()

	start := time.Now()
	resp, _ := s.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ShellExec{
			ShellExec: &codev0.ShellExecRequest{
				Command:        "sleep 30",
				TimeoutSeconds: 1,
			},
		},
	})
	elapsed := time.Since(start)
	r := extractShellExec(t, resp)

	if !r.TimedOut {
		t.Errorf("expected TimedOut=true, got false")
	}
	// Should finish well before the sleep would (30s) — within ~4s
	// (1s timeout + up to 2s graceful + margin).
	if elapsed > 5*time.Second {
		t.Errorf("timeout took %v, should be <5s", elapsed)
	}
}

// TestShellExec_TimeoutKillsGroup spawns a parent shell that backgrounds
// a sleep and then sleeps itself. On timeout, BOTH the parent and its
// backgrounded child must die — that's the whole point of the process
// group kill.
func TestShellExec_TimeoutKillsGroup(t *testing.T) {
	s, dir := newShellExecServer(t)
	ctx := context.Background()
	pidFile := filepath.Join(dir, "childpid")

	resp, _ := s.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ShellExec{
			ShellExec: &codev0.ShellExecRequest{
				// Spawn a backgrounded sleep, record its PID to a file,
				// then sleep ourselves. On timeout, both must die.
				Command:        "sleep 30 & echo $! > childpid; sleep 30",
				TimeoutSeconds: 1,
			},
		},
	})
	r := extractShellExec(t, resp)
	if !r.TimedOut {
		t.Fatalf("expected TimedOut=true")
	}

	// Read the child's PID, give the kill a moment to propagate, and
	// verify the child is gone.
	data, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatalf("pidfile not written: %v", err)
	}
	childPID := 0
	_, err = readInt(strings.TrimSpace(string(data)), &childPID)
	if err != nil || childPID <= 0 {
		t.Fatalf("parse childpid %q: %v", data, err)
	}

	// Poll for up to 2s for the child to die.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !pidAlive(childPID) {
			return // success
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Errorf("backgrounded child PID %d still alive after timeout kill", childPID)
}

// ──────────────────────────────────────────────────────────
// Output bounded
// ──────────────────────────────────────────────────────────

func TestShellExec_OutputBounded(t *testing.T) {
	s, _ := newShellExecServer(t)
	ctx := context.Background()

	// Produce ~8 MiB of stdout; we should see only ~2 MiB + truncation.
	resp, _ := s.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ShellExec{
			ShellExec: &codev0.ShellExecRequest{
				// yes, head -c 8M => exactly 8,388,608 bytes
				Command:        "yes | head -c 8388608",
				TimeoutSeconds: 10,
			},
		},
	})
	r := extractShellExec(t, resp)

	if r.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%q", r.ExitCode, r.Stderr)
	}
	if len(r.Stdout) > shellExecMaxOutputBytes+100 {
		t.Errorf("stdout length %d exceeds bound %d", len(r.Stdout), shellExecMaxOutputBytes)
	}
	if !strings.Contains(r.Stdout, "(truncated)") {
		t.Errorf("expected truncation marker in stdout")
	}
}

// ──────────────────────────────────────────────────────────
// Environment override
// ──────────────────────────────────────────────────────────

func TestShellExec_EnvOverride(t *testing.T) {
	s, _ := newShellExecServer(t)
	ctx := context.Background()

	resp, _ := s.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ShellExec{
			ShellExec: &codev0.ShellExecRequest{
				Command: "echo $MIND_TEST_VAR",
				Env:     []string{"MIND_TEST_VAR=xyzzy"},
			},
		},
	})
	r := extractShellExec(t, resp)

	if !strings.Contains(r.Stdout, "xyzzy") {
		t.Errorf("env var not propagated: %q", r.Stdout)
	}
}

// ──────────────────────────────────────────────────────────
// Missing operation arguments
// ──────────────────────────────────────────────────────────

func TestShellExec_EmptyCommandAndArgs(t *testing.T) {
	s, _ := newShellExecServer(t)
	ctx := context.Background()

	resp, _ := s.Execute(ctx, &codev0.CodeRequest{
		Operation: &codev0.CodeRequest_ShellExec{
			ShellExec: &codev0.ShellExecRequest{},
		},
	})
	r := extractShellExec(t, resp)

	if r.Error == "" {
		t.Error("expected error for empty command+args")
	}
}

// ──────────────────────────────────────────────────────────
// test helpers
// ──────────────────────────────────────────────────────────

// readInt is a tiny wrapper around Sscanf to parse an integer from a
// string without pulling in strconv just for this test.
func readInt(s string, out *int) (int, error) {
	if s == "" {
		return 0, nil
	}
	// Hand-roll to avoid import churn.
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, nil
		}
		n = n*10 + int(r-'0')
	}
	*out = n
	return 1, nil
}

// pidAlive returns true if the given PID is alive. Uses signal 0
// probing, which is the standard Unix "is this process there" check.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}
