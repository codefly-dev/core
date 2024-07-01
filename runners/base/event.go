package base

import observabilityv0 "github.com/codefly-dev/core/generated/go/codefly/observability/v0"

// Event represents data of a **running** service
// Generic so most fields will be nil
type Event struct {
	// Err is the state of error of the service
	Err error

	// Status is the state of the service
	ProcessState

	// CPU
	*observabilityv0.CPU

	// Memory
	*observabilityv0.Memory
}
