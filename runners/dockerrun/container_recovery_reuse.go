package dockerrun

import (
	"fmt"

	"github.com/docker/docker/api/types/container"
)

// validateContainerRecoveryReuse judges the reuse and fingerprint-replacement
// paths that name-based lookup cannot. IsContainerPresent has already proven
// Codefly ownership and an identical exact scope before this runs, so repeating
// those comparisons here only produced unreachable branches and a second error
// message for a refusal it had already made. What genuinely remains is the
// host-binding namespace and the immutability of an ephemeral owner: labels
// cannot be rewritten, so adopting an ephemeral container would leave its old
// creator recorded and let recovery remove it while its adopter lives.
func validateContainerRecoveryReuse(existing, desired *container.Config) error {
	if existing == nil {
		return fmt.Errorf("existing container has no verifiable configuration")
	}
	// A container created before the namespace label existed carries none, and
	// Docker cannot add one after creation. Treating that absence as foreign
	// would refuse to reuse every pre-upgrade container — including the stateful
	// databases the reuse path exists to preserve across CLI restarts. A label
	// that is present and different is a genuinely foreign host or PID namespace.
	if existingNamespace := existing.Labels[LabelCodeflyRecoveryNamespace]; existingNamespace != "" &&
		existingNamespace != desired.Labels[LabelCodeflyRecoveryNamespace] {
		return fmt.Errorf("existing container belongs to another host or PID namespace")
	}
	if (existing.Labels[LabelCodeflyEphemeral] == labelTrue || desired.Labels[LabelCodeflyEphemeral] == labelTrue) &&
		existing.Labels[LabelCodeflySession] != desired.Labels[LabelCodeflySession] {
		return fmt.Errorf("ephemeral container belongs to another agent; its ownership cannot be transferred")
	}
	return nil
}
