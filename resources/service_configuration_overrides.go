package resources

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// ServiceConfigurationOverridesEnvironment is the private SDK-to-CLI carrier
// for invocation-scoped service configuration values.
//
// ARCHITECTURE: service configuration belongs to Codefly, not to the caller's
// process environment. This carrier lets integration harnesses provision a
// real dependency graph without writing developer-local configuration files.
// The local configuration loader validates the target service and merges the
// values before agents receive their typed Configuration objects.
const ServiceConfigurationOverridesEnvironment = "CODEFLY__SERVICE_CONFIGURATION_OVERRIDES"

// ServiceConfigurationOverride is one invocation-scoped value owned by a
// concrete module/service. Secret selects the runtime secret namespace; the
// value remains invocation-local either way.
type ServiceConfigurationOverride struct {
	Service string `json:"service"`
	Name    string `json:"name"`
	Key     string `json:"key"`
	Value   string `json:"value"`
	Secret  bool   `json:"secret"`
}

// EncodeServiceConfigurationOverrides validates and deterministically
// serializes invocation-scoped service values. Duplicate normalized
// coordinates are rejected so option order can never choose a credential.
func EncodeServiceConfigurationOverrides(overrides []ServiceConfigurationOverride) (string, error) {
	if len(overrides) == 0 {
		return "", nil
	}
	ordered, err := validateServiceConfigurationOverrides(overrides)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(ordered)
	if err != nil {
		return "", fmt.Errorf("encode service configuration overrides: %w", err)
	}
	return string(encoded), nil
}

func validateServiceConfigurationOverrides(overrides []ServiceConfigurationOverride) ([]ServiceConfigurationOverride, error) {
	ordered := slices.Clone(overrides)
	slices.SortFunc(ordered, func(a, b ServiceConfigurationOverride) int {
		if compared := strings.Compare(a.Service, b.Service); compared != 0 {
			return compared
		}
		if compared := strings.Compare(a.Name, b.Name); compared != 0 {
			return compared
		}
		return strings.Compare(a.Key, b.Key)
	})
	seen := make(map[string]struct{}, len(ordered))
	for _, override := range ordered {
		service, err := ParseServiceWithOptionalModule(strings.TrimSpace(override.Service))
		if err != nil || strings.TrimSpace(service.Module) == "" || strings.TrimSpace(service.Name) == "" {
			return nil, fmt.Errorf("service configuration override requires a module/service target: %q", override.Service)
		}
		if strings.TrimSpace(override.Name) == "" {
			return nil, fmt.Errorf("service configuration override %q name is required", override.Service)
		}
		if strings.TrimSpace(override.Key) == "" {
			return nil, fmt.Errorf("service configuration override %s/%s key is required", override.Service, override.Name)
		}
		coordinate := UniqueToKey(service.Unique()) + "\x00" + NameToKey(override.Name) + "\x00" + NameToKey(override.Key)
		if _, exists := seen[coordinate]; exists {
			return nil, fmt.Errorf("duplicate service configuration override %s/%s/%s", override.Service, override.Name, override.Key)
		}
		seen[coordinate] = struct{}{}
	}
	return ordered, nil
}

// DecodeServiceConfigurationOverrides decodes and validates the private
// process carrier. Symmetric validation prevents hand-authored environment
// values from bypassing the SDK contract.
func DecodeServiceConfigurationOverrides(encoded string) ([]ServiceConfigurationOverride, error) {
	if strings.TrimSpace(encoded) == "" {
		return nil, nil
	}
	var overrides []ServiceConfigurationOverride
	if err := json.Unmarshal([]byte(encoded), &overrides); err != nil {
		return nil, fmt.Errorf("decode service configuration overrides: %w", err)
	}
	validated, err := validateServiceConfigurationOverrides(overrides)
	if err != nil {
		return nil, err
	}
	return validated, nil
}
