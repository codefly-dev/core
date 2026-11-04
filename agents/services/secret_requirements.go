package services

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
)

// SecretRequirementsContract requires the agent to declare, on its
// BuildCapabilities probe, every secret configuration group the loaded service
// must carry before the requested environment can be rendered — and to derive
// that declaration from the same code its render refuses on, so the two cannot
// disagree.
//
// It is an operation contract rather than an inference from an agent's name or
// version: an agent built before this field exists returns an empty list, and
// an empty list from an agent that does not advertise the contract means "could
// not answer", never "requires none". Reading those two the same way is how a
// readiness check reports ready for a render the agent then refuses.
const SecretRequirementsContract = "secret-requirements/v1"

// MissingSecretConfiguration is one secret configuration group a service's
// agent requires and the environment does not deliver, with only the missing
// keys of that group. A group delivered with some of its keys is reported with
// the remainder, because the agent refuses on the first key it cannot resolve
// and a caller fixing one key at a time learns the set one render at a time.
type MissingSecretConfiguration struct {
	// Service is the service's unique, as a host spells it (module/service).
	Service string
	// Configuration is the secret configuration group name the agent named.
	Configuration string
	// Keys are the group's keys the environment does not deliver, sorted.
	Keys []string
	// EnvironmentKeys are the same keys in the spelling the agent's refusal and
	// the rendered manifests use (CODEFLY__SERVICE_SECRET_CONFIGURATION__…),
	// index-aligned with Keys.
	EnvironmentKeys []string
}

// String names the group and its missing keys, never a value.
func (missing MissingSecretConfiguration) String() string {
	return fmt.Sprintf("%s: secret configuration %q is missing %s",
		missing.Service, missing.Configuration, strings.Join(missing.Keys, ", "))
}

// MissingServiceSecretConfigurations reports every secret configuration key the
// loaded service's agent requires for profile that delivered does not carry.
//
// delivered is the full environment-key spelling of every service-secret key
// the environment will hand this service, from whichever source supplies it —
// a committed or generated secret configuration file, a declared generator, a
// remote key in the environment's store. The comparison is on those keys and
// not on files, so a key an environment mints at render time counts as
// delivered and a cold checkout is not reported as broken.
//
// It returns an error when the agent does not advertise
// SecretRequirementsContract: the caller then knows the requirement was not
// established and can say so, instead of recording an empty result as a clean
// bill. Every other outcome is data: the slice is empty when nothing is
// missing.
func MissingServiceSecretConfigurations(
	serviceUnique string,
	profile builderv0.KubernetesOutputProfile,
	capabilities *builderv0.BuildCapabilitiesResponse,
	delivered []string,
) ([]MissingSecretConfiguration, error) {
	if !slices.Contains(capabilities.GetExecutionContracts(), SecretRequirementsContract) {
		return nil, fmt.Errorf("builder must advertise %s to declare the secret configurations a render requires", SecretRequirementsContract)
	}
	have := make(map[string]bool, len(delivered))
	for _, key := range delivered {
		have[key] = true
	}
	var missing []MissingSecretConfiguration
	for _, required := range capabilities.GetRequiredSecretConfigurations() {
		if !requiredForProfile(required, profile) {
			continue
		}
		entry := MissingSecretConfiguration{Service: serviceUnique, Configuration: required.GetConfiguration()}
		for _, key := range required.GetKeys() {
			environmentKey := resources.ServiceSecretConfigurationKeyFromUnique(serviceUnique, required.GetConfiguration(), key)
			if have[environmentKey] {
				continue
			}
			entry.Keys = append(entry.Keys, key)
			entry.EnvironmentKeys = append(entry.EnvironmentKeys, environmentKey)
		}
		if len(entry.Keys) == 0 {
			continue
		}
		sortKeysTogether(&entry)
		missing = append(missing, entry)
	}
	sort.Slice(missing, func(i, j int) bool {
		if missing[i].Service == missing[j].Service {
			return missing[i].Configuration < missing[j].Configuration
		}
		return missing[i].Service < missing[j].Service
	})
	return missing, nil
}

// requiredForProfile reports whether a declared requirement applies to the
// profile about to be rendered. An agent that names no profile requires the
// group in every one.
func requiredForProfile(required *builderv0.RequiredSecretConfiguration, profile builderv0.KubernetesOutputProfile) bool {
	profiles := required.GetProfiles()
	return len(profiles) == 0 || slices.Contains(profiles, profile)
}

// sortKeysTogether orders the missing keys and keeps EnvironmentKeys aligned to
// them, so a caller may print either list and read the other at the same index.
func sortKeysTogether(entry *MissingSecretConfiguration) {
	order := make([]int, len(entry.Keys))
	for index := range order {
		order[index] = index
	}
	sort.Slice(order, func(i, j int) bool { return entry.Keys[order[i]] < entry.Keys[order[j]] })
	keys := make([]string, len(order))
	environmentKeys := make([]string, len(order))
	for index, from := range order {
		keys[index], environmentKeys[index] = entry.Keys[from], entry.EnvironmentKeys[from]
	}
	entry.Keys, entry.EnvironmentKeys = keys, environmentKeys
}

// ValidateRequiredSecretConfiguration refuses a declaration a host cannot act
// on: an unnamed group, or a group with no keys. An agent advertising the
// contract and declaring an empty group tells the host a credential is required
// without naming it, which is worse than declaring nothing.
func ValidateRequiredSecretConfiguration(required *builderv0.RequiredSecretConfiguration) error {
	if strings.TrimSpace(required.GetConfiguration()) == "" {
		return fmt.Errorf("a required secret configuration must name its group")
	}
	if len(required.GetKeys()) == 0 {
		return fmt.Errorf("required secret configuration %q must name its keys", required.GetConfiguration())
	}
	for _, key := range required.GetKeys() {
		if strings.TrimSpace(key) == "" {
			return fmt.Errorf("required secret configuration %q names an empty key", required.GetConfiguration())
		}
	}
	return nil
}
