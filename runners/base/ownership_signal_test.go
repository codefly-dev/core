//go:build darwin || linux

package base

import (
	"context"
	"errors"
	"path/filepath"
	"syscall"
	"testing"
)

func TestSignalProcessRefusesAnotherBirthAndSignalsUnregisteredProcess(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	target := registryHelperCommand("member")
	target.Env = append(target.Env, processGroupReadyFileEnv+"="+ready)
	target.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := target.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Process.Kill(); _ = target.Wait() })
	waitForTestFile(t, ready)
	identity, err := InspectProcess(target.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	stale := identity
	stale.StartID++
	if err := SignalProcess(t.Context(), stale, syscall.SIGKILL); !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("stale birth: %v", err)
	}
	if err := target.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("stale birth harmed target: %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := SignalProcess(cancelled, identity, syscall.SIGKILL); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled signal: %v", err)
	}
	if err := SignalProcess(t.Context(), ProcessIdentity{PID: identity.PID}, syscall.SIGKILL); err == nil {
		t.Fatal("accepted PID without birth")
	}
	if err := SignalProcess(t.Context(), identity, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	if err := target.Wait(); err != nil {
		t.Fatal(err)
	}
	if err := SignalProcess(t.Context(), identity, syscall.SIGKILL); !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("expired birth: %v", err)
	}
}
