//go:build linux

package base

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestInspectProcessLeaderExitIsNotAbsence(t *testing.T) {
	// A pthread fixture exercises the real kernel state without terminating a
	// Go runtime thread underneath its scheduler. Linux test hosts need cc.
	binary := filepath.Join(t.TempDir(), "leader-exit")
	build := exec.CommandContext(t.Context(), "cc", "-pthread", "-Wall", "-Wextra", "-Werror", "-o", binary, "testdata/leader-exit/main.c")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("compile pthread fixture: %v\n%s", err, output)
	}
	target := exec.CommandContext(t.Context(), binary)
	input, err := target.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	output, err := target.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer output.Close()
	if err := target.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = target.Process.Kill(); _ = target.Wait() })
	// Bound both readiness and the post-leader-exit liveness response.
	if err := output.(*os.File).SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	readByte := func(want byte) {
		t.Helper()
		var b [1]byte
		if _, err := io.ReadFull(output, b[:]); err != nil {
			t.Fatal(err)
		}
		if b[0] != want {
			t.Fatalf("fixture response %q, want %q", b[0], want)
		}
	}
	readByte('R')
	pid := target.Process.Pid
	before, err := InspectProcess(pid)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := input.Write([]byte("X")); err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join("/proc", strconv.Itoa(pid), "exe")
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := os.Readlink(exe)
		if os.IsNotExist(err) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("leader did not exit")
		}
		time.Sleep(time.Millisecond)
	}
	stillPresent, err := readLinuxProcessStat(pid)
	if err != nil {
		t.Fatal(err)
	}
	if stillPresent.startID != before.StartID {
		t.Fatal("fixture birth changed")
	}
	_, inspectionErr := InspectProcess(pid)
	// Prove there is a live worker, rather than merely an unreaped zombie,
	// before asserting how inspection classified the missing executable.
	if _, err := input.Write([]byte("P")); err != nil {
		t.Fatal(err)
	}
	readByte('P')
	if inspectionErr == nil || errors.Is(inspectionErr, ErrProcessNotFound) {
		t.Fatalf("live worker with missing executable must be uncertain, got %v", inspectionErr)
	}
	if err := target.Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectProcess(pid); !errors.Is(err, ErrProcessNotFound) {
		t.Fatalf("reaped process must be absent, got %v", err)
	}
}
