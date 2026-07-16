package sbom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strings"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

type swiftDependency struct {
	Identity     string            `json:"identity"`
	Name         string            `json:"name"`
	URL          string            `json:"url"`
	Version      string            `json:"version"`
	Dependencies []swiftDependency `json:"dependencies"`
}

// Swift inventories the concrete SwiftPM graph while forcing versions from
// Package.resolved. SwiftPM may fetch missing checkouts, but it cannot relock or
// silently select newer versions.
func Swift(ctx context.Context, dir string) (*Result, error) {
	cmd := exec.CommandContext(ctx, "swift", "package", "--package-path", dir,
		"--only-use-versions-from-resolved-file", "show-dependencies", "--format", "json")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if _, lookupErr := exec.LookPath("swift"); lookupErr != nil {
			return nil, fmt.Errorf("%w: swift is required", ErrUnsupported)
		}
		return nil, fmt.Errorf("swift resolved dependency graph: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseSwiftDependencies(stdout.Bytes())
}

func parseSwiftDependencies(data []byte) (*Result, error) {
	var graph swiftDependency
	if err := json.Unmarshal(data, &graph); err != nil {
		return nil, fmt.Errorf("parse swift dependency graph: %w", err)
	}
	if graph.Name == "" && graph.Identity == "" {
		return nil, fmt.Errorf("swift dependency graph has no root package")
	}
	root := swiftComponent(graph, agentv0.ComponentType_MODULE)
	byRef := map[string]*agentv0.Component{}
	edges := map[string][]string{}
	var walk func(swiftDependency)
	walk = func(node swiftDependency) {
		ref := swiftPackageRef(node)
		for _, dependency := range node.Dependencies {
			dependencyRef := swiftPackageRef(dependency)
			edges[ref] = append(edges[ref], dependencyRef)
			if _, ok := byRef[dependencyRef]; !ok {
				byRef[dependencyRef] = swiftComponent(dependency, agentv0.ComponentType_LIBRARY)
				walk(dependency)
			}
		}
	}
	walk(graph)
	delete(byRef, root.BomRef)
	refs := make([]string, 0, len(byRef))
	for ref := range byRef {
		refs = append(refs, ref)
	}
	sort.Strings(refs)
	components := make([]*agentv0.Component, 0, len(refs))
	dependencies := make([]*agentv0.Dependency, 0, len(refs)+1)
	dependencies = append(dependencies, &agentv0.Dependency{Ref: root.BomRef, DependsOn: edges[root.BomRef]})
	for _, ref := range refs {
		components = append(components, byRef[ref])
		dependencies = append(dependencies, &agentv0.Dependency{Ref: ref, DependsOn: edges[ref]})
	}
	return finish(root, components, dependencies, "swift-package-resolved", "SWIFT")
}

func swiftComponent(pkg swiftDependency, componentType agentv0.ComponentType) *agentv0.Component {
	return &agentv0.Component{
		Name:    first(pkg.Identity, pkg.Name),
		Version: normalizedSwiftVersion(pkg.Version),
		Type:    componentType,
		Purl:    swiftPackageRef(pkg),
		BomRef:  swiftPackageRef(pkg),
	}
}

func swiftPackageRef(pkg swiftDependency) string {
	name := first(pkg.Identity, pkg.Name)
	purl := "pkg:swift/" + url.PathEscape(name)
	if version := normalizedSwiftVersion(pkg.Version); version != "" {
		purl += "@" + url.PathEscape(version)
	}
	if pkg.URL != "" && strings.HasPrefix(pkg.URL, "http") {
		purl += "?repository_url=" + url.QueryEscape(pkg.URL)
	}
	return purl
}

func normalizedSwiftVersion(version string) string {
	if version == "unspecified" {
		return ""
	}
	return version
}
