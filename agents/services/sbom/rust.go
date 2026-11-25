package sbom

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sort"
	"strings"

	agentv1 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v1"
	"github.com/codefly-dev/core/internal/cargometadata"
)

type cargoPackage = cargometadata.Package

// Rust inventories Cargo's locked, fully resolved package graph. --locked
// prevents metadata collection from changing Cargo.lock. Dev-only edges and
// components are removed when includeDev is false.
func Rust(ctx context.Context, dir string, includeDev bool) (*Result, error) {
	metadata, err := cargometadata.Run(ctx, dir, cargometadata.Options{Locked: true})
	if err != nil {
		if errors.Is(err, cargometadata.ErrUnavailable) {
			return nil, fmt.Errorf("%w: cargo is required", ErrUnsupported)
		}
		return nil, err
	}
	return cargoMetadataResult(metadata, dir, includeDev)
}

func parseCargoMetadata(data []byte, dir string, includeDev bool) (*Result, error) {
	metadata, err := cargometadata.Parse(data)
	if err != nil {
		return nil, err
	}
	return cargoMetadataResult(metadata, dir, includeDev)
}

func cargoMetadataResult(metadata cargometadata.Metadata, dir string, includeDev bool) (*Result, error) {
	if metadata.Resolve == nil || len(metadata.WorkspaceMembers) == 0 {
		return nil, fmt.Errorf("cargo metadata has no resolved workspace graph")
	}

	workspace := make(map[string]struct{}, len(metadata.WorkspaceMembers))
	for _, id := range metadata.WorkspaceMembers {
		workspace[id] = struct{}{}
	}
	packages := make(map[string]cargoPackage, len(metadata.Packages))
	refs := make(map[string]string, len(metadata.Packages))
	for _, pkg := range metadata.Packages {
		packages[pkg.ID] = pkg
		refs[pkg.ID] = cargoPackageRef(pkg)
	}

	edges := make(map[string][]string, len(metadata.Resolve.Nodes))
	for _, node := range metadata.Resolve.Nodes {
		for _, dependency := range node.Deps {
			if includeDev || cargoDependencyIsRuntime(dependency.DepKinds) {
				edges[node.ID] = append(edges[node.ID], dependency.Pkg)
			}
		}
	}
	rootIDs := append([]string(nil), metadata.WorkspaceMembers...)
	if metadata.Resolve.Root != nil {
		rootIDs = []string{*metadata.Resolve.Root}
	} else if len(metadata.WorkspaceMembers) == 1 {
		rootIDs = []string{metadata.WorkspaceMembers[0]}
	}
	reachable := cargoReachable(rootIDs, edges)

	var root *agentv1.Component
	rootID := ""
	if len(rootIDs) == 1 {
		rootID = rootIDs[0]
		pkg, ok := packages[rootID]
		if !ok {
			return nil, fmt.Errorf("cargo metadata root %q has no package", rootID)
		}
		root = cargoComponent(pkg, agentv1.ComponentType_MODULE)
	} else {
		name := filepath.Base(filepath.Clean(dir))
		root = &agentv1.Component{
			Name:   name,
			Type:   agentv1.ComponentType_MODULE,
			Purl:   "pkg:generic/" + url.PathEscape(name),
			BomRef: "pkg:generic/" + url.PathEscape(name),
		}
	}

	ids := make([]string, 0, len(reachable))
	for id := range reachable {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	components := make([]*agentv1.Component, 0, len(ids))
	dependencies := make([]*agentv1.Dependency, 0, len(ids)+1)
	for _, id := range ids {
		pkg, ok := packages[id]
		if !ok {
			continue
		}
		if id != rootID {
			componentType := agentv1.ComponentType_LIBRARY
			if _, ok := workspace[id]; ok {
				componentType = agentv1.ComponentType_MODULE
			}
			components = append(components, cargoComponent(pkg, componentType))
		}
		var dependsOn []string
		for _, dependencyID := range edges[id] {
			if _, ok := reachable[dependencyID]; ok && refs[dependencyID] != "" {
				dependsOn = append(dependsOn, refs[dependencyID])
			}
		}
		dependencies = append(dependencies, &agentv1.Dependency{Ref: refs[id], DependsOn: dependsOn})
	}
	if rootID == "" {
		var roots []string
		for _, id := range rootIDs {
			if refs[id] != "" {
				roots = append(roots, refs[id])
			}
		}
		dependencies = append(dependencies, &agentv1.Dependency{Ref: root.BomRef, DependsOn: roots})
	}
	return finish(root, components, dependencies, "cargo-metadata", "RUST")
}

func cargoDependencyIsRuntime(kinds []cargometadata.DependencyKind) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, kind := range kinds {
		if kind.Kind == nil || *kind.Kind != "dev" {
			return true
		}
	}
	return false
}

func cargoReachable(roots []string, edges map[string][]string) map[string]struct{} {
	reachable := make(map[string]struct{})
	queue := append([]string(nil), roots...)
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if _, ok := reachable[id]; ok {
			continue
		}
		reachable[id] = struct{}{}
		queue = append(queue, edges[id]...)
	}
	return reachable
}

func cargoComponent(pkg cargoPackage, componentType agentv1.ComponentType) *agentv1.Component {
	component := &agentv1.Component{
		Name:    pkg.Name,
		Version: pkg.Version,
		Type:    componentType,
		Purl:    cargoPackageRef(pkg),
		BomRef:  cargoPackageRef(pkg),
	}
	if pkg.License != nil && *pkg.License != "" {
		component.Licenses = []string{*pkg.License}
	}
	if pkg.Checksum != nil && *pkg.Checksum != "" {
		component.Hashes = []*agentv1.Hash{{Algorithm: "SHA-256", Content: strings.ToLower(*pkg.Checksum)}}
	}
	return component
}

func cargoPackageRef(pkg cargoPackage) string {
	purl := "pkg:cargo/" + url.PathEscape(pkg.Name) + "@" + url.PathEscape(pkg.Version)
	if pkg.Source != nil && *pkg.Source != "" && !strings.HasPrefix(*pkg.Source, "registry+") {
		purl += "?vcs_url=" + url.QueryEscape(strings.TrimPrefix(*pkg.Source, "git+"))
	}
	return purl
}
