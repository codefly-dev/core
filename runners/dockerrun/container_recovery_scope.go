// Package dockerrun manages Docker-backed runner environments and scoped recovery.
package dockerrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/codefly-dev/core/sdk/session"
)

const LabelCodeflyRecoveryScope = "codefly.recovery-scope"
const LabelCodeflyRecoveryGroup = "codefly.recovery-group"
const LabelCodeflyRecoveryNamespace = "codefly.recovery-namespace"
const ContainerRecoveryScopeEnvironment = "CODEFLY_CONTAINER_RECOVERY_SCOPE"

// ContainerRecoveryScopeHeader acknowledges the ownership identity inherited
// by an agent. Older agents omit it and cannot promise scoped recovery.
const ContainerRecoveryScopeHeader = "codefly-container-recovery-scope"

// Capture the launching parent before serving any requests. A parent dying
// during a request must not revoke the child's already inherited ownership.
// If it died before initialization, validation fails and creation is refused.
var containerRecoveryParentPID = os.Getppid()

// InheritedContainerRecoveryScope is the validated identity used by container
// creation and by the agent's read-only gRPC ownership acknowledgement. It is
// empty unless both the exact scope and the durable namespace are inherited:
// an older CLI that projects only a scope cannot promise cross-scope recovery.
func InheritedContainerRecoveryScope() string {
	scope, err := inheritedContainerRecoveryScope()
	if err != nil || scope.id == "" || scope.namespace == "" {
		return ""
	}
	return scope.id + ":" + scope.namespace
}

// ContainerRecoveryScope binds cleanup to a home, workspace and resolved naming
// scope. group additionally covers the disposable siblings of one SDK
// invocation, and namespace is the durable canonical home/workspace identity
// that survives a run choosing an entirely fresh naming scope.
type ContainerRecoveryScope struct{ id, group, namespace string }

func NewContainerRecoveryScope(home, workspace, namingScope string) (ContainerRecoveryScope, error) {
	hostID, err := containerRecoveryHostID()
	if err != nil {
		return ContainerRecoveryScope{}, fmt.Errorf("resolve container recovery host: %w", err)
	}
	return newContainerRecoveryScope(home, workspace, namingScope, hostID)
}

func newContainerRecoveryScope(home, workspace, namingScope, hostID string) (ContainerRecoveryScope, error) {
	if hostID == "" {
		return ContainerRecoveryScope{}, fmt.Errorf("container recovery requires a stable host identity")
	}
	paths := []string{home, workspace}
	for i, path := range paths {
		if path == "" {
			return ContainerRecoveryScope{}, fmt.Errorf("container recovery requires home and workspace paths")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return ContainerRecoveryScope{}, err
		}
		paths[i], err = filepath.EvalSymlinks(absolute)
		if err != nil {
			return ContainerRecoveryScope{}, err
		}
	}
	scope := ContainerRecoveryScope{
		id:        recoveryScopeHash(paths, namingScope),
		namespace: recoveryNamespaceHash(hostID, paths),
	}
	// Only an SDK invocation in disposable mode can delegate recovery across
	// invocation names. Never infer this from a name suffix alone: a regular
	// developer scope may happen to have the same spelling.
	if EphemeralContainers() {
		invocation := session.FromEnvironment(os.Environ())
		if invocation != nil {
			identity, err := hex.DecodeString(invocation.ID)
			if err == nil && len(identity) == 16 {
				suffix := invocation.Scope()
				if namingScope == suffix {
					scope.group = recoveryScopeHash(paths, "")
				} else if prefix, ok := strings.CutSuffix(namingScope, "-"+suffix); ok {
					scope.group = recoveryScopeHash(paths, prefix)
				}
			}
		}
	}
	return scope, nil
}

func recoveryScopeHash(paths []string, namingScope string) string {
	data, _ := json.Marshal(append(paths, namingScope))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// recoveryNamespaceHash covers every naming scope under one canonical
// home/workspace pair, so an aborted run's disposable containers stay
// recoverable by a successor that picked a completely different scope. The host
// identity is part of it: identical paths on a shared Docker daemon do not
// imply the same owner host, and a foreign host's PIDs are not ours to judge.
func recoveryNamespaceHash(hostID string, paths []string) string {
	data, _ := json.Marshal(append([]string{hostID}, paths...))
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// SetContainerRecoveryScope projects ownership to directly spawned agents.
// Older agents omit the label and are deliberately ineligible for startup cleanup.
func SetContainerRecoveryScope(scope ContainerRecoveryScope) error {
	if scope.id == "" || scope.namespace == "" {
		return fmt.Errorf("container recovery scope is unresolved")
	}
	// pid:scope:namespace[:group] — group is optional, so it trails the
	// namespace every resolved scope carries.
	marker := strconv.Itoa(os.Getpid()) + ":" + scope.id + ":" + scope.namespace
	if scope.group != "" {
		marker += ":" + scope.group
	}
	return os.Setenv(ContainerRecoveryScopeEnvironment, marker)
}

func inheritedContainerRecoveryScope() (ContainerRecoveryScope, error) {
	marker := os.Getenv(ContainerRecoveryScopeEnvironment)
	if marker == "" {
		return ContainerRecoveryScope{}, nil
	}
	owner, identity, ok := strings.Cut(marker, ":")
	if !ok {
		return ContainerRecoveryScope{}, fmt.Errorf("invalid container recovery marker")
	}
	pid, err := strconv.Atoi(owner)
	if err != nil || pid <= 1 || (pid != os.Getpid() && pid != containerRecoveryParentPID) {
		return ContainerRecoveryScope{}, fmt.Errorf("container recovery marker does not belong to this process or its launching parent")
	}
	digests := strings.Split(identity, ":")
	if len(digests) > 3 {
		return ContainerRecoveryScope{}, fmt.Errorf("invalid container recovery identity")
	}
	for _, digest := range digests {
		decoded, decodeErr := hex.DecodeString(digest)
		if decodeErr != nil || len(decoded) != sha256.Size {
			return ContainerRecoveryScope{}, fmt.Errorf("invalid container recovery identity")
		}
	}
	// An older CLI projects only the exact scope. Preserve its label, but never
	// acknowledge cross-scope recovery without a validated namespace.
	scope := ContainerRecoveryScope{id: digests[0]}
	if len(digests) > 1 {
		scope.namespace = digests[1]
	}
	if len(digests) > 2 {
		scope.group = digests[2]
	}
	return scope, nil
}
