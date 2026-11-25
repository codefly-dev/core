package base

import observabilityv1 "github.com/codefly-dev/core/generated/go/codefly/observability/v1"

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
