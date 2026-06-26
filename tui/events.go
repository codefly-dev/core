package tui

import "github.com/codefly-dev/core/wool"

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

// ServicePlanMsg lists every service this run manages, in dependency order
// (dependencies first, origin last). It seeds the live status header/footer so
// "what's next" can be shown before any service has started.
type ServicePlanMsg struct {
	Services []string
}

// ServiceFailedMsg flips a single service to Failed in the live status without
// implying the whole run is over (that is ServiceErrorMsg / FlowDoneMsg). A
// dependency can fail and be retried while the rest of the plan still stands.
type ServiceFailedMsg struct {
	Service string
}

// StopPlanMsg tells the runner which dependency services it is managing
// alongside the origin, so the shutdown view can name exactly what gets torn
// down on quit (origin + these dependencies — none stay alive).
type StopPlanMsg struct {
	Dependencies []string
}
