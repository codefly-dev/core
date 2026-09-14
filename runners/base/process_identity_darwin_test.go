package base

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

func TestDarwinProcessNotFoundClassifiesProcessTableRaces(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "gopsutil sentinel", err: process.ErrorProcessNotRunning, want: true},
		{name: "no such process", err: syscall.ESRCH, want: true},
		{name: "unlinked executable", err: syscall.ENOENT, want: false},
		{name: "unrelated failure", err: errors.New("permission denied"), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := darwinProcessNotFound(test.err); got != test.want {
				t.Fatalf("darwinProcessNotFound(%v) = %t, want %t", test.err, got, test.want)
			}
		})
	}
}

func TestInspectDarwinProcessAuthenticatesUnlinkedRunningExecutable(t *testing.T) {
	executable, command, readyPath := startCopiedRegistryHelper(t)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	waitForTestFile(t, readyPath)
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		identity, inspectErr := inspectProcess(command.Process.Pid)
		if inspectErr == nil {
			if identity.pid != command.Process.Pid || identity.executable == "" {
				t.Fatalf("unlinked process identity = %+v", identity)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("inspect unlinked running executable: %v", inspectErr)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestReapStaleProcessGroupsTerminatesAuthenticatedUnlinkedExecutable(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	executable, command, readyPath := startCopiedRegistryHelper(t)
	_, err := StartTrackedProcessGroup(command)
	if err != nil {
		t.Fatal(err)
	}
	waitForTestFile(t, readyPath)
	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	path := recordPath(t, command.Process.Pid)
	rewriteRecord(t, path, func(record *pgidRecord) {
		record.Owner.PID = 1 << 30
		record.Owner.StartID = 1
		record.Owner.Executable = "exited-owner"
	})
	waited := make(chan error, 1)
	go func() {
		waited <- command.Wait()
	}()

	if err := ReapStaleProcessGroups(t.Context()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for reaped process")
	}
	assertGroupDead(t, command.Process.Pid)
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("reaped record still exists: %v", err)
	}
}

// TestDarwinSignalHandleDeliversAcrossExec pins delivery against Darwin's
// p_idversion, which advances on exec as well as on fork. A handle opened
// before the target execs holds a token naming an incarnation that no longer
// exists, and the kernel rejects it with ESRCH — which signalProcessIdentities
// reads as "this member already exited" and passes over, so a live process
// survives a signal pass that reported no failure.
func TestDarwinSignalHandleDeliversAcrossExec(t *testing.T) {
	cmd := exec.Command("sh", "-c", "IFS= read -r release; exec sleep 300")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("StdinPipe: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	reaped := make(chan struct{})
	go func() { _ = cmd.Wait(); close(reaped) }()
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		select {
		case <-reaped:
		case <-time.After(10 * time.Second):
			t.Error("helper was never reaped")
		}
	})

	identity, err := inspectProcess(cmd.Process.Pid)
	if err != nil {
		t.Fatalf("inspectProcess: %v", err)
	}
	handle, err := openProcessSignalHandle(identity)
	if err != nil {
		t.Fatalf("openProcessSignalHandle: %v", err)
	}
	defer func() { _ = handle.Close() }()
	before, err := readDarwinProcessUniqueInfo(identity.pid)
	if err != nil {
		t.Fatalf("readDarwinProcessUniqueInfo: %v", err)
	}

	if _, err := stdin.Write([]byte("release\n")); err != nil {
		t.Fatalf("release the helper into its exec: %v", err)
	}
	if !waitFor(10*time.Second, func() bool {
		unique, uniqueErr := readDarwinProcessUniqueInfo(identity.pid)
		return uniqueErr == nil && unique.PIDVersion != before.PIDVersion
	}) {
		t.Fatal("helper never execed, so the delivery window under test never opened")
	}

	if err := handle.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("Signal after the target execed: %v", err)
	}
	select {
	case <-reaped:
	case <-time.After(10 * time.Second):
		t.Errorf("process %d survived a SIGKILL its handle reported as delivered", identity.pid)
	}
}

func startCopiedRegistryHelper(t *testing.T) (string, *exec.Cmd, string) {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	executable := filepath.Join(directory, "temporary-registry-helper")
	if err := os.WriteFile(executable, payload, 0o700); err != nil {
		t.Fatal(err)
	}
	readyPath := filepath.Join(directory, "ready")
	command := exec.Command(executable, "-test.run=^TestProcessGroupRegistryHelper$")
	command.Env = append(os.Environ(),
		processGroupRoleEnv+"=member",
		processGroupReadyFileEnv+"="+readyPath)
	t.Cleanup(func() {
		if command.Process != nil {
			_ = command.Process.Kill()
			_ = command.Wait()
		}
	})
	return executable, command, readyPath
}
