package resources

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// WorkspaceConfigurationOverridesEnvironment is the private SDK-to-CLI
// carrier for invocation-scoped workspace configuration values.
//
// ARCHITECTURE: persisted developer configuration remains owned by `codefly
// config`. This carrier exists for callers such as integration harnesses that
// start a real dependency graph but must not mutate the developer's persisted
// workspace profile. Values travel only in the spawned CLI process environment
// and are merged by the local configuration loader before service startup.
const WorkspaceConfigurationOverridesEnvironment = "CODEFLY__WORKSPACE_CONFIGURATION_OVERRIDES"

// WorkspaceConfigurationOverride is one invocation-scoped workspace value.
// Secret controls which Codefly runtime namespace carries the value into a
// service; it never changes the value's lifetime or persistence.
type WorkspaceConfigurationOverride struct {
	Name   string `json:"name"`
	Key    string `json:"key"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

// EncodeWorkspaceConfigurationOverrides validates and deterministically
// serializes invocation-scoped workspace values for the private process
// carrier. Duplicate coordinates are rejected instead of depending on option
// ordering for credential selection.
func EncodeWorkspaceConfigurationOverrides(overrides []WorkspaceConfigurationOverride) (string, error) {
	if len(overrides) == 0 {
		return "", nil
	}
	ordered, err := validateWorkspaceConfigurationOverrides(overrides)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return "", fmt.Errorf("encode workspace configuration overrides: %w", err)
	}
	return string(encoded), nil
}

func validateWorkspaceConfigurationOverrides(overrides []WorkspaceConfigurationOverride) ([]WorkspaceConfigurationOverride, error) {
	ordered := slices.Clone(overrides)
	slices.SortFunc(ordered, func(a, b WorkspaceConfigurationOverride) int {
		if compared := strings.Compare(a.Name, b.Name); compared != 0 {
			return compared
		}
		return strings.Compare(a.Key, b.Key)
	})
	seen := make(map[string]struct{}, len(ordered))
	for _, override := range ordered {
		if strings.TrimSpace(override.Name) == "" {
			return nil, fmt.Errorf("workspace configuration override name is required")
		}
		if strings.TrimSpace(override.Key) == "" {
			return nil, fmt.Errorf("workspace configuration override %q key is required", override.Name)
		}
		coordinate := NameToKey(override.Name) + "\x00" + NameToKey(override.Key)
		if _, exists := seen[coordinate]; exists {
			return nil, fmt.Errorf("duplicate workspace configuration override %s/%s", override.Name, override.Key)
		}
		seen[coordinate] = struct{}{}
	}
	return ordered, nil
}

// DecodeWorkspaceConfigurationOverrides decodes and validates the private
// process carrier. Keeping validation symmetric prevents a manually supplied
// environment value from bypassing the SDK contract.
func DecodeWorkspaceConfigurationOverrides(encoded string) ([]WorkspaceConfigurationOverride, error) {
	if strings.TrimSpace(encoded) == "" {
		return nil, nil
	}
	var overrides []WorkspaceConfigurationOverride
	if err := json.Unmarshal([]byte(encoded), &overrides); err != nil {
		return nil, fmt.Errorf("decode workspace configuration overrides: %w", err)
	}
	validated, err := validateWorkspaceConfigurationOverrides(overrides)
	if err != nil {
		return nil, err
	}
	return validated, nil
}
