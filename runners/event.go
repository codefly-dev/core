package runners

import observabilityv1 "github.com/codefly-dev/core/generated/go/observability/v1"

// Event represents data of a **running** service
// Generic so most fields will be nil
type Event struct {
	// Err is the state of error of the service
	Err error

	// Status is the state of the service
	ProcessState

	// CPU
	*observabilityv1.CPU

	// Memory
	*observabilityv1.Memory
}

type ActionType int

const (
	Noop ActionType = iota
	Init
	Start   // Start the service
	Stop    // Stop the service
	Restart // Restart the service
)

// Action represents an action to be taken on a service by the runner
type Action struct {
	Type   ActionType
	Unique string
}
