package composition

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
)

type Acquisition struct {
	Target   string           `json:"target"`
	Owner    ReleaseSelection `json:"owner"`
	Artifact ReleaseArtifact  `json:"artifact"`
}

type ArtifactRequest struct {
	Target string
	Name   string
}

type ResolutionOptions struct {
	ProductRoot           string
	ConfigurationIdentity string
	LocalCheckouts        map[string]string
	SourceBuilds          []string
	Artifacts             []ArtifactRequest
}

type ResolvedComponent struct {
	Target                 string            `json:"target"`
	Inherited              ReleaseSelection  `json:"inherited"`
	Selected               ReleaseSelection  `json:"selected"`
	DefaultOwner           ReleaseSelection  `json:"defaultOwner"`
	Requirements           map[string]string `json:"requirements,omitempty"`
	AdditionalRequirements map[string]string `json:"additionalRequirements,omitempty"`
	Services               []string          `json:"services,omitempty"`
	Provenance             Provenance        `json:"provenance"`
	LocalContent           string            `json:"localContent,omitempty"`
}

type SelectionDifference struct {
	Target       string            `json:"target"`
	Owner        ReleaseSelection  `json:"owner"`
	Inherited    ReleaseSelection  `json:"inherited"`
	Selected     ReleaseSelection  `json:"selected"`
	Requirements map[string]string `json:"requirements"`
	Rationale    string            `json:"rationale"`
}

type ResolutionRecord struct {
	Schema                string                `json:"schema"`
	Product               string                `json:"product"`
	ConfigurationIdentity string                `json:"configurationIdentity"`
	ProductInputIdentity  string                `json:"productInputIdentity"`
	Components            []ResolvedComponent   `json:"components"`
	Differences           []SelectionDifference `json:"differences,omitempty"`
	Acquisitions          []Acquisition         `json:"acquisitions,omitempty"`
	Builds                []BuildRequirement    `json:"builds,omitempty"`
}

type BuildRequirement struct {
	Target           string `json:"target"`
	Service          string `json:"service"`
	AgentTarget      string `json:"agentTarget"`
	SourceIdentity   string `json:"sourceIdentity"`
	ReplacesArtifact string `json:"replacesArtifact"`
}

type ResolvedComposition struct {
	record            ResolutionRecord
	identity          string
	local             map[string]string
	metadata          map[string]*verifiedMetadata
	productRoot       string
	productDescriptor *Descriptor
}

func (resolved *ResolvedComposition) Identity() string { return resolved.identity }

func (resolved *ResolvedComposition) Record() ResolutionRecord {
	data, _ := json.Marshal(resolved.record)
	var record ResolutionRecord
	_ = json.Unmarshal(data, &record)
	return record
}

// ResolveComposition reads only signed declarations. Acquisitions are returned
// to the orchestrator; this path never checks out or builds dependencies.
func (engine *Engine) ResolveComposition(ctx context.Context, descriptor *Descriptor, root ReleaseSelection, options ResolutionOptions) (*ResolvedComposition, error) {
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	if err := validateIncludes("included service", descriptor.Services.Include); err != nil {
		return nil, err
	}
	if err := root.validate(); err != nil {
		return nil, err
	}
	constraint, _ := semver.NewConstraint(descriptor.Base.Version)
	version, _ := semver.StrictNewVersion(root.Version)
	if root.ID != descriptor.Base.ID || !constraint.Check(version) {
		return nil, ErrPackageIdentity
	}
	if !strings.HasPrefix(options.ConfigurationIdentity, "hmac-sha256:") || !digestPattern.MatchString(strings.TrimPrefix(options.ConfigurationIdentity, "hmac-")) {
		return nil, errors.New("protected configuration identity is required")
	}
	resolver, ok := engine.Resolver.(MetadataResolver)
	if !ok {
		return nil, errors.New("resolver does not support signed metadata; dependency source fallback is forbidden")
	}
	resolved := &ResolvedComposition{
		record: ResolutionRecord{Schema: "codefly/composition-selection/v1", Product: descriptor.Name, ConfigurationIdentity: options.ConfigurationIdentity},
		local:  make(map[string]string), metadata: make(map[string]*verifiedMetadata),
	}
	product := *descriptor
	product.Modules, product.Services, product.Replacements = ModuleInstances{}, Services{}, nil
	data, err := json.Marshal(product)
	if err != nil {
		return nil, err
	}
	product = Descriptor{}
	if err := json.Unmarshal(data, &product); err != nil {
		return nil, err
	}
	slices.SortFunc(product.Bindings, func(a, b Binding) int { return strings.Compare(a.Plugin+"/"+a.Alias, b.Plugin+"/"+b.Alias) })
	if len(contributionPaths(&product)) > 0 && !filepath.IsAbs(options.ProductRoot) {
		return nil, errors.New("product contributions require an absolute product root")
	}
	resolved.productRoot, resolved.productDescriptor = options.ProductRoot, &product
	resolved.record.ProductInputIdentity, err = CompositionDigest(options.ProductRoot, &product)
	if err != nil {
		return nil, err
	}
	replacements := make(map[string]Replacement)
	for _, replacement := range descriptor.Replacements {
		replacements[replacement.Target] = replacement
	}
	visited := make(map[string]bool)
	active := make(map[ReleaseSelection]bool)
	cache := make(map[ReleaseSelection]*verifiedMetadata)
	var visit func(string, ComponentDefault, ReleaseSelection, ModuleInstances, Services, int) error
	visit = func(target string, inherited ComponentDefault, owner ReleaseSelection, modules ModuleInstances, services Services, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if depth > 64 || len(resolved.record.Components) >= 4096 {
			return errors.New("composition exceeds the maximum dependency depth or instance count")
		}
		selected := inherited.Release
		requirements := maps.Clone(inherited.Requirements)
		replacement, replaced := replacements[target]
		if replaced {
			if replacement.Release.ID != inherited.Release.ID {
				return fmt.Errorf("%s: replacement cannot redefine component identity or release authority", target)
			}
			selected = replacement.Release
		}
		if active[selected] {
			return fmt.Errorf("dependency cycle at %s (%s@%s)", target, selected.ID, selected.Version)
		}
		active[selected] = true
		defer delete(active, selected)
		metadata, exists := cache[selected]
		if !exists {
			raw, err := resolver.ResolveMetadata(ctx, ResolveRequest{Package: selected.ID, Version: selected.Version})
			if err != nil {
				return fmt.Errorf("%s: acquire release metadata: %w", target, err)
			}
			metadata, err = verifyMetadata(raw, selected, engine.Trust)
			if err != nil {
				return fmt.Errorf("%s: authenticate release metadata: %w", target, err)
			}
			cache[selected] = metadata
		}
		manifest := metadata.manifest
		component := ResolvedComponent{Target: target, Inherited: inherited.Release, Selected: selected, DefaultOwner: owner, Requirements: requirements, Provenance: *metadata.provenance}
		component.AdditionalRequirements = maps.Clone(replacement.Requirements)
		component.Services = slices.Clone(services.Include)
		sort.Strings(component.Services)
		if source, local := options.LocalCheckouts[target]; local {
			if target != "" && strings.HasSuffix(target, "/agent") {
				return fmt.Errorf("%s: local checkouts must target modules", target)
			}
			if !filepath.IsAbs(source) {
				return fmt.Errorf("%s: local checkout must be an absolute path", target)
			}
			before, err := sourceTreeDigest(source)
			if err != nil {
				return err
			}
			manifest, err = LoadPackageManifest(source)
			if err != nil {
				return err
			}
			if manifest.ID != selected.ID {
				return ErrPackageIdentity
			}
			after, err := sourceTreeDigest(source)
			if err != nil || before != after {
				return fmt.Errorf("%s: local checkout changed during resolution: %w", target, errors.Join(err, ErrDigestMismatch))
			}
			component.LocalContent = before
			resolved.local[target] = source
		}
		for _, required := range []map[string]string{inherited.Requirements, replacement.Requirements} {
			for name, value := range required {
				constraint, _ := semver.NewConstraint(value)
				provided, err := semver.StrictNewVersion(manifest.Provides[name])
				if err != nil || !constraint.Check(provided) {
					return fmt.Errorf("%w: %s requires %s %s; selected component provides %q", ErrContract, target, name, value, manifest.Provides[name])
				}
			}
		}
		visited[target] = true
		resolved.metadata[target] = &verifiedMetadata{manifest: manifest, provenance: metadata.provenance, attestation: metadata.attestation}
		resolved.record.Components = append(resolved.record.Components, component)
		if replaced {
			resolved.record.Differences = append(resolved.record.Differences, SelectionDifference{Target: target, Owner: owner, Inherited: inherited.Release, Selected: selected, Requirements: maps.Clone(replacement.Requirements), Rationale: replacement.Rationale})
		}
		prefix := target
		if prefix != "" {
			prefix += "/"
		}
		for _, name := range modules.Include {
			index := slices.IndexFunc(manifest.Modules, func(module ProvidedModule) bool { return module.Name == name })
			if index < 0 {
				return fmt.Errorf("%s: included module %s is not declared", target, name)
			}
			module := manifest.Modules[index]
			if err := visit(prefix+"modules/"+name, module.Default, selected, module.Modules, module.Services, depth+1); err != nil {
				return err
			}
		}
		for _, name := range services.Include {
			index := slices.IndexFunc(manifest.Services, func(service ProvidedService) bool { return service.Name == name })
			if index < 0 {
				return fmt.Errorf("%s: included service %s is not declared", target, name)
			}
			service := manifest.Services[index]
			if len(service.RuntimeArtifacts) == 0 {
				return fmt.Errorf("%s/services/%s: released runtime artifacts are missing; source fallback is forbidden", prefix, name)
			}
			agentTarget := prefix + "services/" + name + "/agent"
			if service.Agent != nil {
				if err := visit(agentTarget, *service.Agent, selected, ModuleInstances{}, Services{}, depth+1); err != nil {
					return err
				}
				if service.AgentUsage == "lifecycle" {
					agent := resolved.metadata[agentTarget]
					selection := ReleaseSelection{ID: agent.manifest.ID, Version: agent.manifest.Version, Digest: agent.provenance.ArtifactDigest}
					found := false
					for _, artifact := range agent.manifest.ReleaseArtifacts {
						if artifact.Purpose == ArtifactLifecycleAgent {
							found = true
							if err := resolved.acquire(agentTarget, artifact.Name, ArtifactLifecycleAgent, selection); err != nil {
								return err
							}
						}
					}
					if !found {
						return fmt.Errorf("%s: lifecycle agent artifact is missing", agentTarget)
					}
				}
			}
			agentReplacement, hasAgentReplacement := replacements[agentTarget]
			agentChanged := service.Agent != nil && service.AgentUsage == "build" && hasAgentReplacement && agentReplacement.Release != service.Agent.Release
			build := component.LocalContent != "" || slices.Contains(options.SourceBuilds, target) || agentChanged
			if build {
				if err := resolved.planServiceBuild(component, service, agentTarget, options); err != nil {
					return err
				}
			} else {
				for _, artifact := range service.RuntimeArtifacts {
					if err := resolved.acquire(target, artifact, ArtifactRuntime, selected); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	if err := visit("", ComponentDefault{Release: root}, root, descriptor.Modules, descriptor.Services, 0); err != nil {
		return nil, err
	}
	for target := range replacements {
		if !visited[target] {
			return nil, fmt.Errorf("replacement target %s does not participate in the composition", target)
		}
	}
	for target := range options.LocalCheckouts {
		if !visited[target] {
			return nil, fmt.Errorf("local checkout target %s does not participate in the composition", target)
		}
	}
	for _, target := range options.SourceBuilds {
		if !visited[target] || strings.HasSuffix(target, "/agent") {
			return nil, fmt.Errorf("source-build target %s is not a participating module", target)
		}
	}
	for _, request := range options.Artifacts {
		if !visited[request.Target] {
			return nil, fmt.Errorf("artifact target %s does not participate in the composition", request.Target)
		}
		metadata := resolved.metadata[request.Target]
		index := slices.IndexFunc(metadata.manifest.ReleaseArtifacts, func(artifact ReleaseArtifact) bool { return artifact.Name == request.Name })
		if index < 0 {
			return nil, fmt.Errorf("%s: required artifact %s is not published; source fallback is forbidden", request.Target, request.Name)
		}
		artifact := metadata.manifest.ReleaseArtifacts[index]
		if artifact.Purpose == ArtifactSource && !slices.Contains(options.SourceBuilds, request.Target) {
			return nil, fmt.Errorf("%s: source acquisition requires an explicit source-build target", request.Target)
		}
		for _, component := range resolved.record.Components {
			if component.Target == request.Target {
				if err := resolved.acquire(request.Target, request.Name, artifact.Purpose, component.Selected); err != nil {
					return nil, err
				}
			}
		}
	}
	sort.Slice(resolved.record.Components, func(i, j int) bool {
		return resolved.record.Components[i].Target < resolved.record.Components[j].Target
	})
	sort.Slice(resolved.record.Differences, func(i, j int) bool {
		return resolved.record.Differences[i].Target < resolved.record.Differences[j].Target
	})
	sort.Slice(resolved.record.Acquisitions, func(i, j int) bool {
		left, right := resolved.record.Acquisitions[i], resolved.record.Acquisitions[j]
		if left.Target != right.Target {
			return left.Target < right.Target
		}
		return left.Artifact.Name < right.Artifact.Name
	})
	sort.Slice(resolved.record.Builds, func(i, j int) bool {
		left, right := resolved.record.Builds[i], resolved.record.Builds[j]
		if left.Target != right.Target {
			return left.Target < right.Target
		}
		if left.Service != right.Service {
			return left.Service < right.Service
		}
		return left.ReplacesArtifact < right.ReplacesArtifact
	})
	data, err = json.Marshal(resolved.record)
	if err != nil {
		return nil, err
	}
	resolved.identity = fmt.Sprintf("sha256:%x", sha256.Sum256(data))
	if err := resolved.CheckLocalInputs(); err != nil {
		return nil, err
	}
	return resolved, nil
}

func (resolved *ResolvedComposition) planServiceBuild(component ResolvedComponent, service ProvidedService, agentTarget string, options ResolutionOptions) error {
	if service.Agent == nil || service.AgentUsage != "build" {
		return fmt.Errorf("%s: source build requires a declared build agent", component.Target)
	}
	manifest := resolved.metadata[component.Target].manifest
	sourceIdentity := component.LocalContent
	if sourceIdentity == "" {
		if !slices.Contains(options.SourceBuilds, component.Target) {
			return fmt.Errorf("%s: changed build agent requires an explicit source-build target", agentTarget)
		}
		if !manifest.AllowDerivedBuilds {
			return fmt.Errorf("%s: owner release does not permit derived builds", component.Target)
		}
		for _, artifact := range manifest.ReleaseArtifacts {
			if artifact.Purpose != ArtifactSource {
				continue
			}
			if sourceIdentity != "" {
				return fmt.Errorf("%s: source-build artifact is ambiguous", component.Target)
			}
			sourceIdentity = artifact.Digest
			if err := resolved.acquire(component.Target, artifact.Name, ArtifactSource, component.Selected); err != nil {
				return err
			}
		}
		if sourceIdentity == "" {
			return fmt.Errorf("%s: released source artifact is missing", component.Target)
		}
	}
	agent := resolved.metadata[agentTarget]
	agentSelection := ReleaseSelection{ID: agent.manifest.ID, Version: agent.manifest.Version, Digest: agent.provenance.ArtifactDigest}
	found := false
	for _, artifact := range agent.manifest.ReleaseArtifacts {
		if artifact.Purpose != ArtifactBuildAgent {
			continue
		}
		found = true
		if err := resolved.acquire(agentTarget, artifact.Name, ArtifactBuildAgent, agentSelection); err != nil {
			return err
		}
	}
	if !found {
		return fmt.Errorf("%s: released build-agent artifact is missing", agentTarget)
	}
	for _, name := range service.RuntimeArtifacts {
		for _, build := range resolved.record.Builds {
			if build.Target == component.Target && build.ReplacesArtifact == name {
				return fmt.Errorf("%s: runtime output %s is shared by multiple build targets", component.Target, name)
			}
		}
		resolved.record.Builds = append(resolved.record.Builds, BuildRequirement{Target: component.Target, Service: service.Name, AgentTarget: agentTarget, SourceIdentity: sourceIdentity, ReplacesArtifact: name})
	}
	return nil
}

func (resolved *ResolvedComposition) acquire(target, name string, purpose ArtifactPurpose, owner ReleaseSelection) error {
	manifest := resolved.metadata[target].manifest
	index := slices.IndexFunc(manifest.ReleaseArtifacts, func(artifact ReleaseArtifact) bool { return artifact.Name == name })
	if index < 0 || manifest.ReleaseArtifacts[index].Purpose != purpose {
		return fmt.Errorf("%s: required %s artifact %s is not published; source fallback is forbidden", target, purpose, name)
	}
	if _, local := resolved.local[target]; local {
		return fmt.Errorf("%s: local source cannot claim released artifact %s; build it with explicit development tooling", target, name)
	}
	for _, acquisition := range resolved.record.Acquisitions {
		if acquisition.Target == target && acquisition.Artifact.Name == name {
			return nil
		}
	}
	resolved.record.Acquisitions = append(resolved.record.Acquisitions, Acquisition{Target: target, Owner: owner, Artifact: manifest.ReleaseArtifacts[index]})
	return nil
}
