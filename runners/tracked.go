package runners

import (
	"context"
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
