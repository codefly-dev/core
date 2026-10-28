package resources

import (
	"crypto/sha256"
	"fmt"
	"path"
	"strings"
	"unicode/utf8"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// ValidateModuleArtifactReference validates a declaration without resolving a
// module. The selected-module owner must confine and qualify the actual read.
func ValidateModuleArtifactReference(ref *basev0.ConfigurationModuleArtifactReference) error {
	if ref == nil || ref.Module == "" || strings.TrimSpace(ref.Module) != ref.Module ||
		strings.ContainsAny(ref.Module, "/\\\x00\r\n") || ref.Path == "" || path.IsAbs(ref.Path) ||
		path.Clean(ref.Path) != ref.Path || ref.Path == "." || strings.ContainsAny(ref.Path, "\\\x00\r\n") {
		return fmt.Errorf("invalid module artifact reference")
	}
	for _, part := range strings.Split(ref.Path, "/") {
		if part == ".." {
			return fmt.Errorf("module artifact path traverses its selection")
		}
	}
	return nil
}

// ValidateModuleArtifactBytes enforces inline byte transport, not content
// semantics or publicness. Multiline values remain eligible for direct delivery.
func ValidateModuleArtifactBytes(value string) error {
	if len(value) > FileCarrierThreshold || !utf8.ValidString(value) || strings.ContainsRune(value, 0) {
		return fmt.Errorf("module artifact must be bounded UTF-8 without NUL")
	}
	if len(EndpointReferences(value)) != 0 || malformedEndpointMarker(value) || strings.Contains(value, "${profile}") {
		return fmt.Errorf("module artifact requires interpolation")
	}
	return nil
}

// ValidateModuleArtifactValue refuses unresolved declarations and validates
// generated evidence against the actual value before filtering or emission.
func ValidateModuleArtifactValue(value *basev0.ConfigurationValue) error {
	if value == nil {
		return fmt.Errorf("configuration value is nil")
	}
	if value.ModuleArtifact != nil {
		return fmt.Errorf("unresolved module artifact cannot be delivered")
	}
	evidence := value.ResolvedModuleArtifact
	if evidence == nil {
		return nil
	}
	if value.Secret || value.Template != nil {
		return fmt.Errorf("module artifact cannot be secret or templated")
	}
	if err := ValidateModuleArtifactReference(evidence.Reference); err != nil {
		return err
	}
	if err := ValidateModuleArtifactBytes(value.Value); err != nil {
		return err
	}
	identity := evidence.ArtifactIdentity
	sum := sha256.Sum256([]byte(value.Value))
	if identity == nil || identity.Algorithm != basev0.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_SHA256 ||
		identity.SizeBytes != int64(len(value.Value)) || identity.Digest != fmt.Sprintf("%x", sum) {
		return fmt.Errorf("module artifact identity does not match delivered value")
	}
	return nil
}

// ValidateModuleArtifactDelivery checks the unmerged baseline and every new
// contribution before precedence can discard an offer. Baseline is comparison
// state, not a request to emit it again. Equality never authorizes replacement.
func ValidateModuleArtifactDelivery(baseline, contributions []*basev0.Configuration, overrides map[string]string) error {
	type offer struct {
		key, group, raw string
		bound, document bool
	}
	var offers []offer
	for _, batch := range [][]*basev0.Configuration{baseline, contributions} {
		for _, conf := range batch {
			for _, info := range conf.GetInfos() {
				if info == nil {
					return fmt.Errorf("nil configuration information")
				}
				group := artifactCarrier(ConfigurationValueEnvironmentKey(conf, info.Name, &basev0.ConfigurationValue{}))
				if info.Data != nil {
					offers = append(offers, offer{group: group, document: true})
				}
				for _, value := range info.ConfigurationValues {
					if err := ValidateTemplatedConfigurationValue(value); err != nil {
						return err
					}
					offers = append(offers, offer{key: artifactCarrier(ConfigurationValueEnvironmentKey(conf, info.Name, value)), group: group, raw: artifactCarrier(value.Key), bound: value.ResolvedModuleArtifact != nil})
				}
			}
		}
	}
	for i, bound := range offers {
		if !bound.bound {
			continue
		}
		for j, other := range offers {
			if i != j && (bound.key == other.key || bound.key == other.raw || strings.HasPrefix(bound.raw, "CODEFLY__") && (bound.raw == other.key || bound.raw == other.raw) || other.document && bound.group == other.group) {
				return fmt.Errorf("additional configuration offer conflicts with module artifact destination")
			}
		}
		for key := range overrides {
			if artifactCarrier(key) == bound.key || strings.HasPrefix(bound.raw, "CODEFLY__") && artifactCarrier(key) == bound.raw {
				return fmt.Errorf("runtime override conflicts with module artifact destination")
			}
		}
	}
	return nil
}

// The SDK accepts normalized and historical hyphen spelling, secret/public
// fallback namespaces and file carriers. Compare their owner-defined carrier,
// never just the trailing key, and never open an override's file path.
func artifactCarrier(key string) string {
	if IsFileCarrierKey(key) {
		key = CarriedKey(key)
	}
	key = NameToKey(key)
	key = strings.Replace(key, WorkspaceSecretConfigurationPrefix, WorkspaceConfigurationPrefix, 1)
	return strings.Replace(key, ServiceSecretConfigurationPrefix, ServiceConfigurationPrefix, 1)
}
