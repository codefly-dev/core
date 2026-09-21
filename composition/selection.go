package composition

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/codefly-dev/core/artifactexecution"
)

type ReleaseSelection struct {
	ID      string `yaml:"id" json:"id"`
	Version string `yaml:"version" json:"version"`
	Digest  string `yaml:"digest" json:"digest"`
}

func checkProjectionSelections(descriptor *Descriptor, manifest *PackageManifest) error {
	if len(descriptor.Modules.Include) != 0 || len(descriptor.Replacements) != 0 ||
		(manifest != nil && (len(manifest.Modules) != 0 || len(manifest.ReleaseArtifacts) != 0)) {
		return errors.New("declaration-based selections require ResolveComposition and explicit acquisition orchestration; single-package projection cannot apply them")
	}
	return nil
}

type ComponentDefault struct {
	Release      ReleaseSelection  `yaml:"release" json:"release"`
	Requirements map[string]string `yaml:"requirements" json:"requirements"`
}

type ModuleInstances struct {
	Include []string `yaml:"include,omitempty" json:"include,omitempty"`
}

type ProvidedModule struct {
	Name     string           `yaml:"name" json:"name"`
	Default  ComponentDefault `yaml:"default" json:"default"`
	Modules  ModuleInstances  `yaml:"modules,omitempty" json:"modules,omitempty"`
	Services Services         `yaml:"services,omitempty" json:"services,omitempty"`
}

type Replacement struct {
	Target       string            `yaml:"target" json:"target"`
	Release      ReleaseSelection  `yaml:"release" json:"release"`
	Requirements map[string]string `yaml:"requirements,omitempty" json:"requirements,omitempty"`
	Rationale    string            `yaml:"rationale" json:"rationale"`
}

type ArtifactPurpose string

const (
	ArtifactContracts      ArtifactPurpose = "contracts"
	ArtifactClient         ArtifactPurpose = "client"
	ArtifactRuntime        ArtifactPurpose = "runtime"
	ArtifactBuildAgent     ArtifactPurpose = "build-agent"
	ArtifactLifecycleAgent ArtifactPurpose = "lifecycle-agent"
	ArtifactSource         ArtifactPurpose = "source"
)

type ReleaseArtifact struct {
	Name      string          `yaml:"name" json:"name"`
	Purpose   ArtifactPurpose `yaml:"purpose" json:"purpose"`
	URI       string          `yaml:"uri" json:"uri"`
	Digest    string          `yaml:"digest" json:"digest"`
	MediaType string          `yaml:"media-type,omitempty" json:"mediaType,omitempty"`
}

type ArtifactReference struct {
	Target string `yaml:"target,omitempty" json:"target,omitempty"`
	Name   string `yaml:"name" json:"name"`
}

type ArtifactOperation struct {
	Operation string                       `yaml:"operation" json:"operation"`
	Protocol  string                       `yaml:"protocol" json:"protocol"`
	Executor  ArtifactReference            `yaml:"executor" json:"executor"`
	Inputs    map[string]ArtifactReference `yaml:"inputs" json:"inputs"`
	Outputs   map[string]string            `yaml:"outputs" json:"outputs"`
}

func (selection ReleaseSelection) validate() error {
	if err := validateIdentifier("selected component", selection.ID); err != nil {
		return err
	}
	if _, err := semver.StrictNewVersion(selection.Version); err != nil {
		return fmt.Errorf("selected component version: %w", err)
	}
	if !digestPattern.MatchString(selection.Digest) {
		return errors.New("selected component requires an immutable release digest")
	}
	return nil
}

func validateInstanceName(name string) error {
	if err := validateIdentifier("instance name", name); err != nil {
		return err
	}
	if strings.Contains(name, "/") || name == "." || name == ".." {
		return fmt.Errorf("invalid instance name %q", name)
	}
	return nil
}

func validateTarget(target string) error {
	parts := strings.Split(target, "/")
	for len(parts) >= 2 && parts[0] == "modules" {
		if err := validateInstanceName(parts[1]); err != nil {
			return err
		}
		parts = parts[2:]
	}
	if len(parts) == 0 && target != "" {
		return nil
	}
	if len(parts) == 3 && parts[0] == "services" && parts[2] == "agent" {
		return validateInstanceName(parts[1])
	}
	return fmt.Errorf("target %q must name a module instance or a service agent", target)
}

func validateIncludes(kind string, includes []string) error {
	if err := uniqueStrings(kind, includes); err != nil {
		return err
	}
	for _, name := range includes {
		if err := validateInstanceName(name); err != nil {
			return err
		}
	}
	return nil
}

func validateRequirements(requirements map[string]string) error {
	for name, value := range requirements {
		if err := validateIdentifier("component requirement", name); err != nil {
			return err
		}
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("requirement %s must not be empty", name)
		}
		if _, err := semver.NewConstraint(value); err != nil {
			return fmt.Errorf("requirement %s: %w", name, err)
		}
	}
	return nil
}

func (component ComponentDefault) validate() error {
	if err := component.Release.validate(); err != nil {
		return err
	}
	if len(component.Requirements) == 0 {
		return errors.New("component default requires explicit interface/configuration requirements")
	}
	return validateRequirements(component.Requirements)
}

func (descriptor *Descriptor) validateSelections() error {
	if err := validateIncludes("included module", descriptor.Modules.Include); err != nil {
		return err
	}
	seen := make(map[string]bool)
	for _, replacement := range descriptor.Replacements {
		if err := validateTarget(replacement.Target); err != nil {
			return err
		}
		if seen[replacement.Target] {
			return fmt.Errorf("conflicting replacements for %s", replacement.Target)
		}
		seen[replacement.Target] = true
		if err := replacement.Release.validate(); err != nil {
			return err
		}
		if err := validateRequirements(replacement.Requirements); err != nil {
			return err
		}
		if strings.TrimSpace(replacement.Rationale) == "" {
			return fmt.Errorf("replacement %s requires a rationale", replacement.Target)
		}
	}
	return nil
}

func (manifest *PackageManifest) validateSelections() error {
	if manifest.RequiredQualifications != nil {
		if err := uniqueStrings("required qualification", *manifest.RequiredQualifications); err != nil {
			return err
		}
		for _, kind := range *manifest.RequiredQualifications {
			if err := validateIdentifier("required qualification", kind); err != nil {
				return err
			}
		}
	}
	seen := make(map[string]bool)
	for _, module := range manifest.Modules {
		if err := validateInstanceName(module.Name); err != nil {
			return err
		}
		if seen[module.Name] {
			return fmt.Errorf("duplicate module instance %s", module.Name)
		}
		seen[module.Name] = true
		if err := module.Default.validate(); err != nil {
			return err
		}
		if err := validateIncludes("included module", module.Modules.Include); err != nil {
			return err
		}
		if err := validateIncludes("included service", module.Services.Include); err != nil {
			return err
		}
	}
	for _, service := range manifest.Services {
		if service.Agent != nil {
			if service.AgentUsage != "build" && service.AgentUsage != "lifecycle" {
				return fmt.Errorf("service %s must declare agent-usage as build or lifecycle", service.Name)
			}
			if err := validateInstanceName(service.Name); err != nil {
				return err
			}
			if err := service.Agent.validate(); err != nil {
				return err
			}
		} else if service.AgentUsage != "" {
			return fmt.Errorf("service %s declares agent usage without an agent", service.Name)
		}
	}
	for contract, version := range manifest.Provides {
		if err := validateIdentifier("provided contract", contract); err != nil {
			return err
		}
		if _, err := semver.StrictNewVersion(version); err != nil {
			return fmt.Errorf("provided contract %s requires an exact version: %w", contract, err)
		}
	}
	seen = make(map[string]bool)
	for _, artifact := range manifest.ReleaseArtifacts {
		if artifact.MediaType != "" {
			if err := artifactexecution.ValidateMediaType(artifact.MediaType); err != nil {
				return err
			}
		}
		if err := validateInstanceName(artifact.Name); err != nil {
			return err
		}
		if seen[artifact.Name] {
			return fmt.Errorf("duplicate release artifact %s", artifact.Name)
		}
		seen[artifact.Name] = true
		switch artifact.Purpose {
		case ArtifactContracts, ArtifactClient, ArtifactRuntime, ArtifactBuildAgent, ArtifactLifecycleAgent, ArtifactSource:
		default:
			return fmt.Errorf("unsupported artifact purpose %q", artifact.Purpose)
		}
		if err := validateArtifactURI(artifact.URI); err != nil {
			return fmt.Errorf("artifact %s: %w", artifact.Name, err)
		}
		if !digestPattern.MatchString(artifact.Digest) {
			return fmt.Errorf("artifact %s requires a content digest", artifact.Name)
		}
	}
	for _, service := range manifest.Services {
		operations := make(map[string]bool)
		for _, operation := range service.ArtifactOperations {
			if operations[operation.Operation] || (operation.Operation != "build" && operation.Operation != "render") {
				return fmt.Errorf("service %s has invalid or duplicate artifact operation", service.Name)
			}
			operations[operation.Operation] = true
			if operation.Operation == "build" && operation.Protocol != artifactexecution.BuilderBuild || operation.Operation == "render" && operation.Protocol != artifactexecution.BuilderRender && operation.Protocol != artifactexecution.SolutionRender {
				return fmt.Errorf("unsupported artifact operation protocol %s", operation.Protocol)
			}
			if len(operation.Inputs) == 0 || len(operation.Outputs) == 0 {
				return fmt.Errorf("service %s operation requires explicit inputs and outputs", service.Name)
			}
			refs := []ArtifactReference{operation.Executor}
			for name, reference := range operation.Inputs {
				if err := validateInstanceName(name); err != nil {
					return err
				}
				refs = append(refs, reference)
			}
			for _, reference := range refs {
				if reference.Target != "" {
					if err := validateTarget(reference.Target); err != nil {
						return err
					}
				}
				if err := validateInstanceName(reference.Name); err != nil {
					return err
				}
			}
			for name, media := range operation.Outputs {
				if err := validateInstanceName(name); err != nil {
					return err
				}
				if err := artifactexecution.ValidateMediaType(media); err != nil {
					return err
				}
			}
		}
		if err := uniqueStrings("runtime artifact", service.RuntimeArtifacts); err != nil {
			return err
		}
		for _, name := range service.RuntimeArtifacts {
			found := false
			for _, artifact := range manifest.ReleaseArtifacts {
				if artifact.Name == name && artifact.Purpose == ArtifactRuntime {
					found = true
				}
			}
			if !found {
				return fmt.Errorf("service %s requires unpublished runtime artifact %s", service.Name, name)
			}
		}
	}
	return nil
}

func validateArtifactURI(value string) error {
	location, err := url.Parse(value)
	if err != nil || (location.Scheme != "https" && location.Scheme != "oci") || location.Host == "" || location.User != nil || location.RawQuery != "" || location.ForceQuery || location.Fragment != "" {
		return errors.New("artifact requires an HTTPS or OCI location without credentials, query or fragment")
	}
	return nil
}

// ConfigurationIdentity uses a caller-owned secret key so low-entropy secrets
// cannot be recovered by guessing values against a published plain hash.
func ConfigurationIdentity(key []byte, values map[string]string) (string, error) {
	if len(key) < 32 {
		return "", errors.New("configuration identity requires at least a 256-bit key")
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	hash := hmac.New(sha256.New, key)
	writeDigestFrame(hash, []byte("codefly/configuration/v1"))
	writeDigestFrame(hash, data)
	return fmt.Sprintf("hmac-sha256:%x", hash.Sum(nil)), nil
}
