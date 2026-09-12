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
const ContainerRecoveryScopeEnvironment = "CODEFLY_CONTAINER_RECOVERY_SCOPE"

// ContainerRecoveryScope binds cleanup to a home, workspace and resolved naming scope.
type ContainerRecoveryScope struct{ id, group string }

func NewContainerRecoveryScope(home, workspace, namingScope string) (ContainerRecoveryScope, error) {
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

// SetContainerRecoveryScope projects ownership to directly spawned agents.
// Older agents omit the label and are deliberately ineligible for startup cleanup.
func SetContainerRecoveryScope(scope ContainerRecoveryScope) error {
	if scope.id == "" {
		return fmt.Errorf("container recovery scope is unresolved")
	}
	marker := strconv.Itoa(os.Getpid()) + ":" + scope.id
	if scope.group != "" {
		marker += ":" + scope.group
	}
	return os.Setenv(ContainerRecoveryScopeEnvironment, marker)
}

func inheritedContainerRecoveryScope() ContainerRecoveryScope {
	owner, identity, ok := strings.Cut(os.Getenv(ContainerRecoveryScopeEnvironment), ":")
	if !ok {
		return ContainerRecoveryScope{}
	}
	pid, err := strconv.Atoi(owner)
	if err != nil || (pid != os.Getpid() && pid != os.Getppid()) {
		return ContainerRecoveryScope{}
	}
	id, group, grouped := strings.Cut(identity, ":")
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != sha256.Size {
		return ContainerRecoveryScope{}
	}
	if grouped {
		decoded, err = hex.DecodeString(group)
		if err != nil || len(decoded) != sha256.Size {
			return ContainerRecoveryScope{}
		}
	}
	return ContainerRecoveryScope{id: id, group: group}
}
