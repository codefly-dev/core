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
)

const LabelCodeflyRecoveryScope = "codefly.recovery-scope"
const LabelCodeflyRecoveryNamespace = "codefly.recovery-namespace"
const ContainerRecoveryScopeEnvironment = "CODEFLY_CONTAINER_RECOVERY_SCOPE"

// containerRecoveryMarkerVersion tags the marker's field layout. Two Core
// revisions gave the field after the exact scope different meanings — a
// recovery group in one, the durable namespace in the other — and a reader that
// guesses wrong stamps a container with ownership no sweep can ever match,
// leaking exactly what this recovery exists to collect. An untagged marker is
// therefore honored only for the one field every revision agreed on.
const containerRecoveryMarkerVersion = "v2"

// ContainerRecoveryScopeHeader acknowledges the ownership identity inherited
// by an agent. Older agents omit it and cannot promise scoped recovery.
const ContainerRecoveryScopeHeader = "codefly-container-recovery-scope"

// Capture the launching parent before serving any requests. A parent dying
// during a request must not revoke the child's already inherited ownership.
// If it died before initialization, validation fails and creation is refused.
var containerRecoveryParentPID = os.Getppid()

// InheritedContainerRecoveryScope is the validated identity used by container
// creation and by the agent's read-only gRPC ownership acknowledgement. It
// echoes the complete inherited identity, including an empty namespace when the
// CLI's host has no durable identity: the caller compares it against what it
// projected, so conflating "agent did not understand" with "cross-scope recovery
// is unavailable here" would report a compatible agent as incompatible.
func InheritedContainerRecoveryScope() string {
	scope, err := inheritedContainerRecoveryScope()
	if err != nil || scope.id == "" {
		return ""
	}
	return scope.id + ":" + scope.namespace
}

// ContainerRecoveryScope binds cleanup to a home, workspace and resolved naming
// scope; namespace is the durable canonical host/home/workspace identity that
// survives a run choosing an entirely fresh naming scope.
type ContainerRecoveryScope struct{ id, namespace string }

func NewContainerRecoveryScope(home, workspace, namingScope string) (ContainerRecoveryScope, error) {
	hostID, err := containerRecoveryHostID()
	if err != nil {
		return ContainerRecoveryScope{}, fmt.Errorf("resolve container recovery host: %w", err)
	}
	return newContainerRecoveryScope(home, workspace, namingScope, hostID)
}

func newContainerRecoveryScope(home, workspace, namingScope, hostID string) (ContainerRecoveryScope, error) {
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
	scope := ContainerRecoveryScope{id: recoveryScopeHash(paths, namingScope)}
	// A host that cannot prove a durable identity gets no namespace, so
	// cross-scope recovery is unavailable while the exact-scope sweep keeps
	// working. Refusing to resolve a scope at all would stop every run on a
	// Linux host without a provisioned machine ID — which is every common
	// container base image.
	if hostID != "" {
		scope.namespace = recoveryNamespaceHash(hostID, paths)
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
	if scope.id == "" {
		return fmt.Errorf("container recovery scope is unresolved")
	}
	// pid:v2:scope:namespace. The namespace is empty on a host with no durable
	// identity.
	marker := strings.Join([]string{strconv.Itoa(os.Getpid()), containerRecoveryMarkerVersion, scope.id, scope.namespace}, ":")
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
	fields := strings.Split(identity, ":")
	if fields[0] != containerRecoveryMarkerVersion {
		// A marker written before the layout was tagged. Only the exact scope
		// had a single agreed meaning across those revisions, so refuse anything
		// trailing it rather than guess whether it is a group or a namespace.
		if len(fields) != 1 {
			return ContainerRecoveryScope{}, fmt.Errorf("untagged container recovery marker carries ambiguous fields")
		}
		if err := validRecoveryDigest(fields[0]); err != nil {
			return ContainerRecoveryScope{}, fmt.Errorf("invalid container recovery identity")
		}
		return ContainerRecoveryScope{id: fields[0]}, nil
	}
	digests := fields[1:]
	if len(digests) != 2 {
		return ContainerRecoveryScope{}, fmt.Errorf("container recovery marker has %d fields, want scope and namespace", len(digests))
	}
	if err := validRecoveryDigest(digests[0]); err != nil {
		return ContainerRecoveryScope{}, fmt.Errorf("invalid container recovery identity")
	}
	scope := ContainerRecoveryScope{id: digests[0]}
	// The namespace is deliberately optional — a host with no durable identity
	// projects the exact scope alone — but must be a digest when present.
	if digests[1] != "" {
		if err := validRecoveryDigest(digests[1]); err != nil {
			return ContainerRecoveryScope{}, fmt.Errorf("invalid container recovery namespace")
		}
		scope.namespace = digests[1]
	}
	return scope, nil
}

func validRecoveryDigest(digest string) error {
	decoded, err := hex.DecodeString(digest)
	if err != nil || len(decoded) != sha256.Size {
		return fmt.Errorf("not a sha256 digest")
	}
	return nil
}
