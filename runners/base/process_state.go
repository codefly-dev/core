package base

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/codefly-dev/core/wool"

	"github.com/shirou/gopsutil/v3/process"
)

func (ps ProcessState) String() string {
	return [...]string{
		"Unknown",
		"NotFound",
		"Running",
		"Interruptible Sleep",
		"Uninterruptible Sleep",
		"Stopped",
		"Zombie",
		"Dead",
		"Tracing Stop",
		"Idle",
		"Parked",
		"Waking",
	}[ps]
}

type TrackedProcess struct {
	PID    int
	Killed bool
}

func (p *TrackedProcess) GetState(ctx context.Context) (ProcessState, error) {
	w := wool.Get(ctx).In("TrackedProcess.Status", wool.Field("pid", p.PID))
	// Check for PID
	proc, err := os.FindProcess(p.PID)
	if err != nil {
		return NotFound, nil
	}
	// Sending signal 0 to a pid will return an error if the pid is not running
	// and do nothing if it is.
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		state, err := findState(ctx, p.PID)
		if err != nil {
			return Unknown, w.Wrapf(err, "cannot check if proc is defunct")
		}
		return state, nil
	}
	return Dead, nil
}

func (p *TrackedProcess) GetCPU(ctx context.Context) (*CPU, error) {
	w := wool.Get(ctx).In("TrackedProcess.Usage", wool.Field("pid", p.PID))
	proc, err := process.NewProcess(int32(p.PID))
	if err != nil {
		return nil, w.Wrapf(err, "cannot createAndWait process")
	}

	// Get CPU percent
	cpuPercent, err := proc.CPUPercent()
	if err != nil {
		return nil, w.Wrapf(err, "cannot get cpu percent")
	}

	return &CPU{usage: cpuPercent}, nil
}

func (p *TrackedProcess) GetMemory(ctx context.Context) (*Memory, error) {
	w := wool.Get(ctx).In("TrackedProcess.Usage", wool.Field("pid", p.PID))
	proc, err := process.NewProcess(int32(p.PID))
	if err != nil {
		return nil, w.Wrapf(err, "cannot createAndWait process")
	}

	// Get memory info
	memInfo, err := proc.MemoryInfo()
	if err != nil {
		return nil, w.Wrapf(err, "cannot get memory info")
	}
	return &Memory{used: (memInfo.RSS) / 1024.0}, nil
}

func parseState(out string) (string, bool) {
	state := strings.TrimSpace(out)
	// Can we the S+ version
	if regular, ok := strings.CutSuffix(state, "+"); ok {
		return regular, false
	}
	return state, true
}

func findState(ctx context.Context, pid int) (ProcessState, error) {
	w := wool.Get(ctx).In("findState", wool.Field("pid", pid))
	// #nosec G204
	cmd := exec.Command("ps", "-p", fmt.Sprintf("%d", pid), "-o", "state=")
	var out bytes.Buffer
	cmd.Stdout = &out
	err := cmd.Run()
	if err != nil {
		return Unknown, err
	}
	state, tts := parseState(out.String())
	if tts {
		w.Debug("process %d is in TTS")
	}
	switch state {
	case "R":
		return Running, nil
	case "S":
		return InterruptibleSleep, nil
	case "D":
		return UninterruptibleSleep, nil
	case "T":
		return Stopped, nil
	case "Z":
		return Zombie, nil
	case "X":
		return Dead, nil
	case "t":
		return TracingStop, nil
	case "I":
		return Idle, nil
	case "P":
		return Parked, nil
	case "W":
		return Waking, nil
	default:
		return Unknown, w.NewError("unknown state: %s", out.String())
	}
}
