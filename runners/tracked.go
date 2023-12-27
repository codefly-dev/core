package runners

import (
	"context"

	runtimev1 "github.com/codefly-dev/core/generated/go/services/runtime/v1"
)

type Tracked interface {
	GetState(ctx context.Context) (ProcessState, error)
	GetCPU(ctx context.Context) (*CPU, error)
	GetMemory(ctx context.Context) (*Memory, error)
}

type ProcessState int

const (
	Unknown  ProcessState = iota
	NotFound ProcessState = iota
	Running
	InterruptibleSleep
	UninterruptibleSleep
	Stopped
	Zombie
	Dead
	TracingStop
	Idle
	Parked
	Waking
)

type CPU struct {
	usage float64
}

type Memory struct {
	used uint64
}

func NewTracked(tracker *runtimev1.Tracker) (Tracked, error) {
	switch tracker.Tracker.(type) {
	case *runtimev1.Tracker_ProcessTracker:
		return &TrackedProcess{
			PID: int(tracker.Tracker.(*runtimev1.Tracker_ProcessTracker).ProcessTracker.PID),
		}, nil

	default:
		return nil, nil
	}
}
