package tui

import "github.com/codefly-dev/wool"

// ServiceLogMsg carries a log entry from an agent or service binary.
type ServiceLogMsg struct {
	Level   wool.Loglevel
	Source  string
	Message string
}

// ServiceStateMsg reports a lifecycle state change.
type ServiceStateMsg struct {
	State   ServiceState
	Service string
}

// ServiceState enumerates the phases of a service lifecycle.
type ServiceState int

const (
	StateLoading ServiceState = iota
	StateInitializing
	StateStarting
	StateRunning
	StateTesting
	StateStopping
	StateStopped
	StateFailed
)

func (s ServiceState) String() string {
	switch s {
	case StateLoading:
		return "Loading"
	case StateInitializing:
		return "Initializing"
	case StateStarting:
		return "Starting"
	case StateRunning:
		return "Running"
	case StateTesting:
		return "Testing"
	case StateStopping:
		return "Stopping"
	case StateStopped:
		return "Stopped"
	case StateFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

// ServiceReadyMsg is sent when the service has started successfully.
type ServiceReadyMsg struct {
	Service string
	Port    int
}

// ServiceErrorMsg is sent on a fatal error from the flow.
type ServiceErrorMsg struct {
	Err error
}

// FlowDoneMsg signals that the flow has completed (playbook returned).
type FlowDoneMsg struct {
	Err error
}
