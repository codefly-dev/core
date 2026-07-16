package sbom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

type cyclonedxJSON struct {
	Metadata struct {
		Component *cyclonedxComponent `json:"component"`
	} `json:"metadata"`
	Components   []cyclonedxComponent  `json:"components"`
	Dependencies []cyclonedxDependency `json:"dependencies"`
}

type cyclonedxComponent struct {
	Type    string `json:"type"`
	BomRef  string `json:"bom-ref"`
	Group   string `json:"group"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Purl    string `json:"purl"`
	Hashes  []struct {
		Algorithm string `json:"alg"`
		Content   string `json:"content"`
	} `json:"hashes"`
	Licenses []struct {
		Expression string `json:"expression"`
		License    *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"license"`
	} `json:"licenses"`
}

type cyclonedxDependency struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn"`
}

// Python delegates lock interpretation to uv's pinned/frozen CycloneDX 1.5
// exporter. The command is read-only and fails when no authoritative uv.lock
// exists instead of inventorying an unrelated ambient Python environment.
func Python(ctx context.Context, dir string, includeDev bool) (*Result, error) {
	args := []string{"export", "--directory", dir, "--preview-features", "sbom-export", "--format", "cyclonedx1.5", "--frozen"}
	if !includeDev {
		args = append(args, "--no-dev")
	}
	cmd := exec.CommandContext(ctx, "uv", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if _, lookupErr := exec.LookPath("uv"); lookupErr != nil {
			return nil, fmt.Errorf("%w: uv is required", ErrUnsupported)
		}
		return nil, fmt.Errorf("uv CycloneDX export: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseCycloneDX(stdout.Bytes(), "uv-cyclonedx1.5", "PYTHON")
}

func parseCycloneDX(data []byte, tool, language string) (*Result, error) {
	var document cyclonedxJSON
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, fmt.Errorf("parse CycloneDX JSON: %w", err)
	}
	var root *agentv0.Component
	if document.Metadata.Component != nil {
		root = fromCycloneDXComponent(*document.Metadata.Component)
	}
	components := make([]*agentv0.Component, 0, len(document.Components))
	for _, raw := range document.Components {
		component := fromCycloneDXComponent(raw)
		if root == nil && component.GetType() == agentv0.ComponentType_MODULE {
			root = component
			continue
		}
		components = append(components, component)
	}
	if root == nil {
		return nil, fmt.Errorf("CycloneDX document has no root application component")
	}
	dependencies := make([]*agentv0.Dependency, 0, len(document.Dependencies))
	for _, dependency := range document.Dependencies {
		dependencies = append(dependencies, &agentv0.Dependency{Ref: dependency.Ref, DependsOn: dependency.DependsOn})
	}
	return finish(root, components, dependencies, tool, language)
}

func fromCycloneDXComponent(raw cyclonedxComponent) *agentv0.Component {
	component := &agentv0.Component{
		Name:    raw.Name,
		Version: raw.Version,
		Type:    componentType(raw.Type),
		Group:   raw.Group,
		Purl:    raw.Purl,
		BomRef:  first(raw.BomRef, raw.Purl),
	}
	for _, rawHash := range raw.Hashes {
		component.Hashes = append(component.Hashes, &agentv0.Hash{Algorithm: rawHash.Algorithm, Content: strings.ToLower(rawHash.Content)})
	}
	for _, rawLicense := range raw.Licenses {
		license := rawLicense.Expression
		if rawLicense.License != nil {
			license = first(rawLicense.License.ID, rawLicense.License.Name)
		}
		if license != "" {
			component.Licenses = append(component.Licenses, license)
		}
	}
	return component
}

func componentType(value string) agentv0.ComponentType {
	switch strings.ToLower(value) {
	case "application", "module":
		return agentv0.ComponentType_MODULE
	case "framework":
		return agentv0.ComponentType_FRAMEWORK
	case "container":
		return agentv0.ComponentType_CONTAINER
	case "library":
		return agentv0.ComponentType_LIBRARY
	default:
		return agentv0.ComponentType_COMPONENT_TYPE_UNSPECIFIED
	}
}
