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
const ContainerRecoveryScopeEnvironment = "CODEFLY_CONTAINER_RECOVERY_SCOPE"

// ContainerRecoveryScope binds cleanup to a home, workspace and resolved naming scope.
type ContainerRecoveryScope struct{ id string }

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
	data, _ := json.Marshal(append(paths, namingScope))
	sum := sha256.Sum256(data)
	return ContainerRecoveryScope{id: hex.EncodeToString(sum[:])}, nil
}

// SetContainerRecoveryScope projects ownership to directly spawned agents.
// Older agents omit the label and are deliberately ineligible for startup cleanup.
func SetContainerRecoveryScope(scope ContainerRecoveryScope) error {
	if scope.id == "" {
		return fmt.Errorf("container recovery scope is unresolved")
	}
	return os.Setenv(ContainerRecoveryScopeEnvironment, strconv.Itoa(os.Getpid())+":"+scope.id)
}

func inheritedContainerRecoveryScope() string {
	owner, id, ok := strings.Cut(os.Getenv(ContainerRecoveryScopeEnvironment), ":")
	if !ok {
		return ""
	}
	pid, err := strconv.Atoi(owner)
	if err != nil || (pid != os.Getpid() && pid != os.Getppid()) {
		return ""
	}
	decoded, err := hex.DecodeString(id)
	if err != nil || len(decoded) != sha256.Size {
		return ""
	}
	return id
}
