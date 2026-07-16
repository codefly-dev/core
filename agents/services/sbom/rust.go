package sbom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

type cargoMetadata struct {
	Packages []cargoPackage `json:"packages"`
	Resolve  *struct {
		Root  *string     `json:"root"`
		Nodes []cargoNode `json:"nodes"`
	} `json:"resolve"`
	WorkspaceMembers []string `json:"workspace_members"`
}

type cargoPackage struct {
	Name     string  `json:"name"`
	Version  string  `json:"version"`
	ID       string  `json:"id"`
	Source   *string `json:"source"`
	License  *string `json:"license"`
	Checksum *string `json:"checksum"`
}

type cargoNode struct {
	ID   string `json:"id"`
	Deps []struct {
		Pkg      string `json:"pkg"`
		DepKinds []struct {
			Kind *string `json:"kind"`
		} `json:"dep_kinds"`
	} `json:"deps"`
}

// Rust inventories Cargo's locked, fully resolved package graph. --locked
// prevents metadata collection from changing Cargo.lock. Dev-only edges and
// components are removed when includeDev is false.
func Rust(ctx context.Context, dir string, includeDev bool) (*Result, error) {
	cmd := exec.CommandContext(ctx, "cargo", "metadata", "--locked", "--format-version", "1")
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if _, lookupErr := exec.LookPath("cargo"); lookupErr != nil {
			return nil, fmt.Errorf("%w: cargo is required", ErrUnsupported)
		}
		return nil, fmt.Errorf("cargo metadata --locked: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseCargoMetadata(stdout.Bytes(), dir, includeDev)
}

func parseCargoMetadata(data []byte, dir string, includeDev bool) (*Result, error) {
	var metadata cargoMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return nil, fmt.Errorf("parse cargo metadata: %w", err)
	}
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

	var root *agentv0.Component
	rootID := ""
	if len(rootIDs) == 1 {
		rootID = rootIDs[0]
		pkg, ok := packages[rootID]
		if !ok {
			return nil, fmt.Errorf("cargo metadata root %q has no package", rootID)
		}
		root = cargoComponent(pkg, agentv0.ComponentType_MODULE)
	} else {
		name := filepath.Base(filepath.Clean(dir))
		root = &agentv0.Component{
			Name:   name,
			Type:   agentv0.ComponentType_MODULE,
			Purl:   "pkg:generic/" + url.PathEscape(name),
			BomRef: "pkg:generic/" + url.PathEscape(name),
		}
	}

	ids := make([]string, 0, len(reachable))
	for id := range reachable {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	components := make([]*agentv0.Component, 0, len(ids))
	dependencies := make([]*agentv0.Dependency, 0, len(ids)+1)
	for _, id := range ids {
		pkg, ok := packages[id]
		if !ok {
			continue
		}
		if id != rootID {
			componentType := agentv0.ComponentType_LIBRARY
			if _, ok := workspace[id]; ok {
				componentType = agentv0.ComponentType_MODULE
			}
			components = append(components, cargoComponent(pkg, componentType))
		}
		var dependsOn []string
		for _, dependencyID := range edges[id] {
			if _, ok := reachable[dependencyID]; ok && refs[dependencyID] != "" {
				dependsOn = append(dependsOn, refs[dependencyID])
			}
		}
		dependencies = append(dependencies, &agentv0.Dependency{Ref: refs[id], DependsOn: dependsOn})
	}
	if rootID == "" {
		var roots []string
		for _, id := range rootIDs {
			if refs[id] != "" {
				roots = append(roots, refs[id])
			}
		}
		dependencies = append(dependencies, &agentv0.Dependency{Ref: root.BomRef, DependsOn: roots})
	}
	return finish(root, components, dependencies, "cargo-metadata", "RUST")
}

func cargoDependencyIsRuntime(kinds []struct {
	Kind *string `json:"kind"`
}) bool {
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

func cargoComponent(pkg cargoPackage, componentType agentv0.ComponentType) *agentv0.Component {
	component := &agentv0.Component{
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
		component.Hashes = []*agentv0.Hash{{Algorithm: "SHA-256", Content: strings.ToLower(*pkg.Checksum)}}
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
