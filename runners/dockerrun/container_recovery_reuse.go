package dockerrun

import (
	"fmt"

	"github.com/docker/docker/api/types/container"
)

// Labels are immutable. Adopting an ephemeral container would leave its old
// creator recorded as owner, letting recovery remove it while its adopter lives.
// Check ownership before fingerprint-driven replacement as well as reuse.
func validateContainerRecoveryReuse(existing, desired *container.Config) error {
	if existing == nil {
		return fmt.Errorf("existing container has no verifiable configuration")
	}
	if desired.Labels[LabelCodeflyRecoveryScope] == "" {
		if existing.Labels[LabelCodeflyRecoveryScope] != "" || existing.Labels[LabelCodeflyRecoveryNamespace] != "" {
			return fmt.Errorf("unscoped caller cannot claim a container with recovery ownership")
		}
		return nil // Callers that do not participate in scoped recovery.
	}
	if existing.Labels[LabelCodeflyOwner] != labelTrue || existing.Labels[LabelCodeflyRecoveryScope] != desired.Labels[LabelCodeflyRecoveryScope] || existing.Labels[LabelCodeflyRecoveryNamespace] != desired.Labels[LabelCodeflyRecoveryNamespace] {
		return fmt.Errorf("existing container has foreign or unverified recovery ownership")
	}
	if (existing.Labels[LabelCodeflyEphemeral] == labelTrue || desired.Labels[LabelCodeflyEphemeral] == labelTrue) && existing.Labels[LabelCodeflySession] != desired.Labels[LabelCodeflySession] {
		return fmt.Errorf("ephemeral container belongs to another agent; its ownership cannot be transferred")
	}
	return nil
}
