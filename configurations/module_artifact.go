package configurations

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	basev1 "github.com/codefly-dev/core/generated/go/codefly/base/v1"
	"github.com/codefly-dev/core/wire"
	"github.com/codefly-dev/core/resources"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

// ModuleArtifactResolver acquires exact public bytes from an already selected
// module. Selection provenance, confined bounded reads and observed instability
// belong to this caller; Core never fetches a module or calls a secret provider.
type ModuleArtifactResolver func(context.Context, *basev1.ConfigurationModuleArtifactReference) ([]byte, error)

// WithModuleArtifactResolver supplies acquisition for each ordinary Load.
func (local *ConfigurationInformationLocalReader) WithModuleArtifactResolver(resolve ModuleArtifactResolver) *ConfigurationInformationLocalReader {
	local.moduleArtifactResolver = resolve
	return local
}

const moduleArtifactsFile = "module-artifacts.codefly.json"
const maxModuleArtifactDeclarationBytes = 64 << 10

func loadModuleArtifactBindings(ctx context.Context, dir string, infos []*basev1.ConfigurationInformation) ([]*basev1.ConfigurationInformation, error) {
	file, err := os.OpenFile(filepath.Join(dir, moduleArtifactsFile), os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) {
		return infos, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cannot read module artifact declarations")
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		return nil, fmt.Errorf("module artifact declarations must be a regular file")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxModuleArtifactDeclarationBytes+1))
	if err != nil || len(content) > maxModuleArtifactDeclarationBytes || !json.Valid(content) {
		return nil, fmt.Errorf("invalid or oversized module artifact declarations")
	}
	var node yaml.Node
	if yaml.Unmarshal(content, &node) != nil || wire.Check(&node).Kind != wire.NoDefect {
		return nil, fmt.Errorf("invalid module artifact declaration fields")
	}
	var declarations basev1.ProfileModuleArtifactConfigurations
	if protojson.Unmarshal(content, &declarations) != nil || declarations.SchemaVersion != 1 {
		return nil, fmt.Errorf("invalid module artifact declaration schema")
	}
	seen := map[string]bool{}
	for _, binding := range declarations.Bindings {
		if binding == nil || strings.TrimSpace(binding.Configuration) == "" || strings.TrimSpace(binding.Key) == "" || resources.ConfigurationNameHasAmbiguousSpelling(binding.Configuration) {
			return nil, fmt.Errorf("invalid module artifact destination")
		}
		if err := resources.ValidateModuleArtifactReference(binding.Source); err != nil {
			return nil, err
		}
		coordinate := profileValueKey(binding.Configuration, binding.Key)
		if seen[coordinate] {
			return nil, fmt.Errorf("duplicate module artifact destination")
		}
		seen[coordinate] = true
		var group *basev1.ConfigurationInformation
		for _, info := range infos {
			if resources.Match(info.Name, binding.Configuration) {
				group = info
				break
			}
		}
		if group == nil {
			group = &basev1.ConfigurationInformation{Name: binding.Configuration}
			infos = append(infos, group)
		}
		if group.Data != nil {
			return nil, fmt.Errorf("module artifact conflicts with structured configuration: %w", ErrConfigurationConflict)
		}
		if value := findConfigurationValue(group, binding.Key); value != nil && !moduleArtifactSlot(value) {
			return nil, fmt.Errorf("module artifact conflicts with literal: %w", ErrConfigurationConflict)
		}
		value := &basev1.ConfigurationValue{Key: binding.Key, ModuleArtifact: proto.Clone(binding.Source).(*basev1.ConfigurationModuleArtifactReference)}
		if trace := configurationOriginsFrom(ctx); trace != nil {
			if err := trace.record(&basev1.ConfigurationInformation{Name: group.Name, ConfigurationValues: []*basev1.ConfigurationValue{value}}, filepath.Join(dir, moduleArtifactsFile)); err != nil {
				return nil, err
			}
		}
		setConfigurationValue(group, value)
	}
	return infos, nil
}

func resolveModuleArtifacts(ctx context.Context, confs []*basev1.Configuration, resolve ModuleArtifactResolver) error {
	acquired := map[string]string{}
	for _, conf := range confs {
		for _, info := range conf.GetInfos() {
			for _, value := range info.GetConfigurationValues() {
				if value == nil || value.ResolvedModuleArtifact != nil {
					return fmt.Errorf("authored generated module artifact evidence is forbidden")
				}
				ref := value.ModuleArtifact
				if ref == nil {
					continue
				}
				fail := func(reason string) error {
					where := fmt.Sprintf("module artifact %q path %q for %q/%q/%q", ref.Module, ref.Path, conf.Origin, info.Name, value.Key)
					if trace := configurationOriginsFrom(ctx); trace != nil && trace.values[value] != "" {
						where += fmt.Sprintf(" declared in %q", trace.values[value])
					}
					return fmt.Errorf("%s: %s", where, reason)
				}
				if err := resources.ValidateModuleArtifactReference(ref); err != nil {
					return err
				}
				if value.Value != "" || value.Secret || value.Template != nil {
					return fmt.Errorf("module artifact conflicts with literal, secret or template")
				}
				if resolve == nil {
					return fail("selected module artifact requires a resolver")
				}
				key := ref.Module + "\x00" + ref.Path
				content, found := acquired[key]
				if !found {
					data, err := resolve(ctx, proto.Clone(ref).(*basev1.ConfigurationModuleArtifactReference))
					if err != nil {
						return fail("cannot acquire selected module artifact")
					}
					content = string(data) // copied; resolver cannot mutate the delivered bytes
					if err := resources.ValidateModuleArtifactBytes(content); err != nil {
						return fail(err.Error())
					}
					acquired[key] = content
				}
				sum := sha256.Sum256([]byte(content))
				value.Value = content
				value.ResolvedModuleArtifact = &basev1.ResolvedConfigurationModuleArtifact{Reference: proto.Clone(ref).(*basev1.ConfigurationModuleArtifactReference), ArtifactIdentity: &basev1.SourceIdentity{Algorithm: basev1.SourceIdentityAlgorithm_SOURCE_IDENTITY_ALGORITHM_SHA256, Digest: fmt.Sprintf("%x", sum), SizeBytes: int64(len(content))}}
				value.ModuleArtifact = nil
			}
		}
	}
	return resources.ValidateModuleArtifactDelivery(confs, nil, nil)
}

func hasModuleArtifact(value *basev1.ConfigurationValue) bool {
	return value.GetModuleArtifact() != nil || value.GetResolvedModuleArtifact() != nil
}

// Check offers before any whole-group or per-key replacement discards them.
// Reference-to-reference follows ordinary precedence; a profile slot is the
// only ordinary value permitted to yield to a reference.
func checkArtifactOverlay(base, override *basev1.ConfigurationInformation, whole bool) error {
	if base == nil || override == nil {
		return nil
	}
	for direction, pair := range [][2]*basev1.ConfigurationInformation{{base, override}, {override, base}} {
		for _, value := range pair[0].GetConfigurationValues() {
			if !hasModuleArtifact(value) {
				continue
			}
			other := findConfigurationValue(pair[1], value.Key)
			if pair[1].Data != nil || other != nil && !hasModuleArtifact(other) && (direction == 0 || !moduleArtifactSlot(other)) || whole && other == nil {
				return fmt.Errorf("module artifact conflicts with an overwritten configuration offer: %w", ErrConfigurationConflict)
			}
		}
	}
	return nil
}

func checkArtifactOverride(infos []*basev1.ConfigurationInformation, name, key string) error {
	for _, info := range infos {
		if resources.Match(info.Name, name) && hasModuleArtifact(findConfigurationValue(info, key)) {
			return fmt.Errorf("invocation override conflicts with module artifact: %w", ErrConfigurationConflict)
		}
	}
	return nil
}

func moduleArtifactSlot(value *basev1.ConfigurationValue) bool {
	return value != nil && value.Value == ProfileValueMarker && !value.Secret && value.Template == nil && !hasModuleArtifact(value)
}
