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

// ContainerRecoveryScopeHeader acknowledges the ownership identity inherited
// by an agent. Older agents omit it and cannot promise scoped recovery.
const ContainerRecoveryScopeHeader = "codefly-container-recovery-scope"

// Reparenting after a CLI crash must not erase the ownership of containers an
// already-spawned agent is still preparing. Descendants validate their own
// startup parent and cannot reuse a grandparent's marker.
var containerRecoveryParentPID = os.Getppid()

// InheritedContainerRecoveryScope is the validated identity used by container
// creation and by the agent's read-only gRPC ownership acknowledgement.
func InheritedContainerRecoveryScope() string {
	id, namespace := inheritedContainerRecoveryIdentity()
	if id == "" || namespace == "" {
		return ""
	}
	return id + ":" + namespace
}

// ContainerRecoveryScope binds cleanup to a home, workspace and resolved naming scope.
type ContainerRecoveryScope struct {
	id        string
	namespace string
}

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
	root, _ := json.Marshal([]string{hostID, paths[0], paths[1]})
	namespace := sha256.Sum256(root)
	data, _ := json.Marshal(append(paths, namingScope))
	sum := sha256.Sum256(data)
	return ContainerRecoveryScope{id: hex.EncodeToString(sum[:]), namespace: hex.EncodeToString(namespace[:])}, nil
}

// SetContainerRecoveryScope projects ownership to directly spawned agents.
// Older agents omit the label and are deliberately ineligible for startup cleanup.
func SetContainerRecoveryScope(scope ContainerRecoveryScope) error {
	if scope.id == "" || scope.namespace == "" {
		return fmt.Errorf("container recovery scope is unresolved")
	}
	return os.Setenv(ContainerRecoveryScopeEnvironment, strconv.Itoa(os.Getpid())+":"+scope.id+":"+scope.namespace)
}

func inheritedContainerRecoveryScope() string {
	id, _ := inheritedContainerRecoveryIdentity()
	return id
}

func inheritedContainerRecoveryIdentity() (string, string) {
	owner, identity, ok := strings.Cut(os.Getenv(ContainerRecoveryScopeEnvironment), ":")
	if !ok {
		return "", ""
	}
	pid, err := strconv.Atoi(owner)
	if err != nil || (pid != os.Getpid() && pid != containerRecoveryParentPID) {
		return "", ""
	}
	id, namespace, hasNamespace := strings.Cut(identity, ":")
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != sha256.Size {
		return "", ""
	}
	// Older CLIs provide only the exact scope. Preserve its label, but never
	// acknowledge cross-scope recovery without a validated namespace.
	if hasNamespace {
		decoded, err = hex.DecodeString(namespace)
		if err != nil || len(decoded) != sha256.Size {
			return "", ""
		}
	}
	return id, namespace
}
