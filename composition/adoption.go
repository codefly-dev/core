package composition

import (
	"context"
	"errors"
	"fmt"
	"slices"
)

type UpstreamAdoption struct {
	SelectionIdentity     string              `json:"selectionIdentity"`
	OwnerRepository       string              `json:"ownerRepository"`
	Difference            SelectionDifference `json:"difference"`
	InheritedRequirements map[string]string   `json:"inheritedRequirements"`
	DeploymentApproval    string              `json:"deploymentApproval,omitempty"`
	LocalDevelopment      bool                `json:"localDevelopment"`
}

func (resolved *ResolvedComposition) UpstreamAdoptions(approved *ApprovedDeployment) ([]UpstreamAdoption, error) {
	if resolved == nil || resolved.identity == "" {
		return nil, errors.New("resolved composition is required")
	}
	if approved != nil && approved.record.SelectionIdentity != resolved.identity {
		return nil, errors.New("deployment approval belongs to different selections")
	}
	record := resolved.Record()
	var facts []UpstreamAdoption
	for _, difference := range record.Differences {
		fact := UpstreamAdoption{SelectionIdentity: resolved.identity, Difference: difference, LocalDevelopment: len(resolved.local) != 0}
		for _, component := range record.Components {
			if component.Selected == difference.Owner {
				fact.OwnerRepository = component.Provenance.Repository
			}
			if component.Target == difference.Target {
				fact.InheritedRequirements = component.Requirements
			}
		}
		if approved != nil {
			fact.DeploymentApproval = approved.identity
		}
		facts = append(facts, fact)
	}
	return facts, nil
}

type OverrideRemoval struct {
	Target                  string
	PreviousIdentity        string
	WithOverrideIdentity    string
	WithoutOverrideIdentity string
	Descriptor              Descriptor
}

func (engine *Engine) ProposeOverrideRemoval(ctx context.Context, previous *ResolvedComposition, descriptor *Descriptor, candidate ReleaseSelection, options ResolutionOptions, target string) (*OverrideRemoval, error) {
	if previous == nil || previous.identity == "" {
		return nil, errors.New("previous resolved composition is required")
	}
	if err := descriptor.Validate(); err != nil {
		return nil, err
	}
	index := slices.IndexFunc(descriptor.Replacements, func(replacement Replacement) bool { return replacement.Target == target })
	if index < 0 {
		return nil, fmt.Errorf("replacement target %s is not declared", target)
	}
	oldIndex := slices.IndexFunc(previous.record.Differences, func(difference SelectionDifference) bool { return difference.Target == target })
	if oldIndex < 0 || previous.record.Differences[oldIndex].Selected != descriptor.Replacements[index].Release {
		return nil, errors.New("replacement no longer matches the previous resolved selection")
	}
	with, err := engine.ResolveComposition(ctx, descriptor, candidate, options)
	if err != nil {
		return nil, err
	}
	withoutDescriptor := *descriptor
	withoutDescriptor.Replacements = slices.Delete(slices.Clone(descriptor.Replacements), index, index+1)
	without, err := engine.ResolveComposition(ctx, &withoutDescriptor, candidate, options)
	if err != nil {
		return nil, err
	}
	if effectiveSelectionIdentity(with.Record()) != effectiveSelectionIdentity(without.Record()) {
		return nil, errors.New("upstream defaults, requirements or artifacts are not equivalent with the replacement removed")
	}
	return &OverrideRemoval{Target: target, PreviousIdentity: previous.identity, WithOverrideIdentity: with.identity, WithoutOverrideIdentity: without.identity, Descriptor: withoutDescriptor}, nil
}

func effectiveSelectionIdentity(record ResolutionRecord) string {
	record.Differences = nil
	for index := range record.Components {
		record.Components[index].Inherited = ReleaseSelection{}
		record.Components[index].DefaultOwner = ReleaseSelection{}
	}
	return structuredIdentity(record)
}

func (resolved *ResolvedComposition) CheckLocalInputs() error {
	if resolved == nil || resolved.identity == "" {
		return errors.New("resolved composition is required")
	}
	for _, component := range resolved.record.Components {
		source, local := resolved.local[component.Target]
		if !local {
			continue
		}
		digest, err := sourceTreeDigest(source)
		if err != nil {
			return err
		}
		if digest != component.LocalContent {
			return fmt.Errorf("%w: local content for %s changed after resolution", ErrDigestMismatch, component.Target)
		}
	}
	return nil
}
