package resources

import (
	"context"
	"fmt"
	"slices"
	"strings"
)

type RunProfile struct {
	ExcludeDependencies            []string `yaml:"exclude-dependencies,omitempty"`
	ExcludeWorkspaceConfigurations []string `yaml:"exclude-workspace-configurations,omitempty"`
}

type runProfileInventory struct {
	services                map[string]struct{}
	servicesByName          map[string][]string
	workspaceConfigurations map[string]struct{}
}

func (workspace *Workspace) ResolveRunProfile(ctx context.Context, name string, explicit RunProfile) (RunProfile, error) {
	selected := RunProfile{}
	if name != "" {
		if strings.TrimSpace(name) == "" {
			return RunProfile{}, fmt.Errorf("run profile name cannot be empty")
		}
		var ok bool
		selected, ok = workspace.RunProfiles[name]
		if !ok {
			return RunProfile{}, fmt.Errorf("run profile %q is not declared", name)
		}
	}

	combined := RunProfile{
		ExcludeDependencies: append(
			append([]string(nil), selected.ExcludeDependencies...),
			explicit.ExcludeDependencies...,
		),
		ExcludeWorkspaceConfigurations: append(
			append([]string(nil), selected.ExcludeWorkspaceConfigurations...),
			explicit.ExcludeWorkspaceConfigurations...,
		),
	}
	if len(combined.ExcludeDependencies) == 0 && len(combined.ExcludeWorkspaceConfigurations) == 0 {
		return combined, nil
	}

	inventory, err := workspace.loadRunProfileInventory(ctx)
	if err != nil {
		return RunProfile{}, err
	}
	return inventory.resolve(combined)
}

func (workspace *Workspace) validateRunProfiles(ctx context.Context) error {
	names := make([]string, 0, len(workspace.RunProfiles))
	needsInventory := false
	for name, profile := range workspace.RunProfiles {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("run profile name cannot be empty")
		}
		names = append(names, name)
		needsInventory = needsInventory ||
			len(profile.ExcludeDependencies) > 0 ||
			len(profile.ExcludeWorkspaceConfigurations) > 0
	}
	if !needsInventory {
		return nil
	}

	slices.Sort(names)
	inventory, err := workspace.loadRunProfileInventory(ctx)
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, err := inventory.resolve(workspace.RunProfiles[name]); err != nil {
			return fmt.Errorf("run profile %q: %w", name, err)
		}
	}
	return nil
}

func (workspace *Workspace) loadRunProfileInventory(ctx context.Context) (*runProfileInventory, error) {
	services, err := workspace.LoadServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("cannot load services for run profiles: %w", err)
	}

	inventory := &runProfileInventory{
		services:                make(map[string]struct{}, len(services)),
		servicesByName:          make(map[string][]string),
		workspaceConfigurations: make(map[string]struct{}),
	}
	for _, service := range services {
		identity, err := service.Identity()
		if err != nil {
			return nil, fmt.Errorf("cannot identify service for run profiles: %w", err)
		}
		unique := identity.Unique()
		inventory.services[unique] = struct{}{}
		inventory.servicesByName[identity.Name] = append(inventory.servicesByName[identity.Name], unique)
		for _, configuration := range service.WorkspaceConfigurationDependencies {
			inventory.workspaceConfigurations[configuration] = struct{}{}
		}
	}
	for name := range inventory.servicesByName {
		slices.Sort(inventory.servicesByName[name])
	}
	return inventory, nil
}

func (inventory *runProfileInventory) resolve(profile RunProfile) (RunProfile, error) {
	dependencies := make(map[string]struct{}, len(profile.ExcludeDependencies))
	for _, dependency := range profile.ExcludeDependencies {
		canonical, err := inventory.resolveService(dependency)
		if err != nil {
			return RunProfile{}, err
		}
		dependencies[canonical] = struct{}{}
	}

	configurations := make(map[string]struct{}, len(profile.ExcludeWorkspaceConfigurations))
	for _, configuration := range profile.ExcludeWorkspaceConfigurations {
		configuration = strings.TrimSpace(configuration)
		if _, ok := inventory.workspaceConfigurations[configuration]; !ok {
			return RunProfile{}, fmt.Errorf("unknown workspace configuration %q", configuration)
		}
		configurations[configuration] = struct{}{}
	}

	resolved := RunProfile{
		ExcludeDependencies:            make([]string, 0, len(dependencies)),
		ExcludeWorkspaceConfigurations: make([]string, 0, len(configurations)),
	}
	for dependency := range dependencies {
		resolved.ExcludeDependencies = append(resolved.ExcludeDependencies, dependency)
	}
	for configuration := range configurations {
		resolved.ExcludeWorkspaceConfigurations = append(resolved.ExcludeWorkspaceConfigurations, configuration)
	}
	slices.Sort(resolved.ExcludeDependencies)
	slices.Sort(resolved.ExcludeWorkspaceConfigurations)
	return resolved, nil
}

func (inventory *runProfileInventory) resolveService(reference string) (string, error) {
	reference = strings.TrimSpace(reference)
	service, err := ParseServiceWithOptionalModule(reference)
	if err != nil {
		return "", err
	}
	if service.Module != "" {
		unique := service.Unique()
		if _, ok := inventory.services[unique]; !ok {
			return "", fmt.Errorf("unknown service %q", reference)
		}
		return unique, nil
	}

	candidates := inventory.servicesByName[service.Name]
	switch len(candidates) {
	case 0:
		return "", fmt.Errorf("unknown service %q", reference)
	case 1:
		return candidates[0], nil
	default:
		return "", fmt.Errorf("service %q is ambiguous: %s", reference, strings.Join(candidates, ", "))
	}
}
