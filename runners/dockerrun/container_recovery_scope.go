// Package dockerrun manages Docker-backed runner environments and scoped recovery.
package dockerrun

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/codefly-dev/core/runners/recoveryscope"
)

const LabelCodeflyRecoveryScope = "codefly.recovery-scope"
const LabelCodeflyRecoveryNamespace = "codefly.recovery-namespace"

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
	return os.Setenv(recoveryscope.EnvironmentVariable, recoveryscope.Marker(os.Getpid(), scope.id, scope.namespace))
}

func inheritedContainerRecoveryScope() (ContainerRecoveryScope, error) {
	id, namespace, err := recoveryscope.Inherited()
	if err != nil {
		return ContainerRecoveryScope{}, err
	}
	return ContainerRecoveryScope{id: id, namespace: namespace}, nil
}
