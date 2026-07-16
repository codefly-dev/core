package sbom

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"google.golang.org/protobuf/proto"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

// Artifact identifies one compiled agent binary to wrap around a source-module
// inventory. SHA256 is the digest of the exact installed bytes.
type Artifact struct {
	Publisher string
	Name      string
	Version   string
	Target    string
	SHA256    string
}

// AttachArtifact makes the compiled binary the SBOM subject while retaining
// the source module and its dependency graph beneath it.
func AttachArtifact(base *Result, artifact Artifact) (*Result, error) {
	if base == nil || base.Bom == nil || base.Bom.GetMetadata().GetComponent() == nil {
		return nil, fmt.Errorf("source SBOM has no root component")
	}
	bom := proto.Clone(base.Bom).(*agentv0.Bom)
	sourceRoot := bom.Metadata.Component
	components := append(bom.Components, sourceRoot)
	purl := fmt.Sprintf("pkg:generic/%s/%s@%s", url.PathEscape(artifact.Publisher), url.PathEscape(artifact.Name), url.PathEscape(artifact.Version))
	if artifact.Target != "" {
		parts := strings.SplitN(artifact.Target, "/", 2)
		if len(parts) == 2 {
			purl += "?arch=" + url.QueryEscape(parts[1]) + "&os=" + url.QueryEscape(parts[0])
		}
	}
	root := &agentv0.Component{
		Name:    artifact.Name,
		Group:   artifact.Publisher,
		Version: artifact.Version,
		Type:    agentv0.ComponentType_APPLICATION,
		Purl:    purl,
		BomRef:  purl,
		Hashes: []*agentv0.Hash{{
			Algorithm: "SHA-256",
			Content:   strings.ToLower(artifact.SHA256),
		}},
	}
	dependencies := append(bom.Dependencies, &agentv0.Dependency{Ref: root.BomRef, DependsOn: []string{sourceRoot.BomRef}})
	return finish(root, components, dependencies, "codefly-agent-build+"+base.Tool, base.Language)
}

type jsonBom struct {
	BomFormat    string           `json:"bomFormat"`
	SpecVersion  string           `json:"specVersion"`
	SerialNumber string           `json:"serialNumber"`
	Version      int32            `json:"version"`
	Metadata     jsonMetadata     `json:"metadata"`
	Components   []jsonComponent  `json:"components"`
	Dependencies []jsonDependency `json:"dependencies"`
}

type jsonMetadata struct {
	Timestamp string        `json:"timestamp,omitempty"`
	Component jsonComponent `json:"component"`
	Tools     []jsonTool    `json:"tools,omitempty"`
}

type jsonTool struct {
	Vendor  string `json:"vendor,omitempty"`
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type jsonComponent struct {
	Type     string        `json:"type"`
	BomRef   string        `json:"bom-ref"`
	Group    string        `json:"group,omitempty"`
	Name     string        `json:"name"`
	Version  string        `json:"version,omitempty"`
	Purl     string        `json:"purl,omitempty"`
	Hashes   []jsonHash    `json:"hashes,omitempty"`
	Licenses []jsonLicense `json:"licenses,omitempty"`
}

type jsonHash struct {
	Algorithm string `json:"alg"`
	Content   string `json:"content"`
}

type jsonLicense struct {
	Expression string             `json:"expression,omitempty"`
	License    *jsonLicenseChoice `json:"license,omitempty"`
}

type jsonLicenseChoice struct {
	ID string `json:"id"`
}

type jsonDependency struct {
	Ref       string   `json:"ref"`
	DependsOn []string `json:"dependsOn"`
}

// MarshalCycloneDXJSON emits standards-shaped CycloneDX 1.5 JSON. It uses
// structs rather than maps so the byte stream and digest remain deterministic.
func MarshalCycloneDXJSON(bom *agentv0.Bom) ([]byte, error) {
	if bom == nil || bom.GetMetadata().GetComponent() == nil {
		return nil, fmt.Errorf("CycloneDX BOM has no metadata component")
	}
	document := jsonBom{
		BomFormat:    bom.GetBomFormat(),
		SpecVersion:  bom.GetSpecVersion(),
		SerialNumber: bom.GetSerialNumber(),
		Version:      bom.GetVersion(),
		Metadata: jsonMetadata{
			Timestamp: bom.GetMetadata().GetTimestamp(),
			Component: toJSONComponent(bom.GetMetadata().GetComponent()),
		},
	}
	for _, tool := range bom.GetMetadata().GetTools() {
		document.Metadata.Tools = append(document.Metadata.Tools, jsonTool{Vendor: tool.GetVendor(), Name: tool.GetName(), Version: tool.GetVersion()})
	}
	for _, component := range bom.GetComponents() {
		document.Components = append(document.Components, toJSONComponent(component))
	}
	for _, dependency := range bom.GetDependencies() {
		document.Dependencies = append(document.Dependencies, jsonDependency{Ref: dependency.GetRef(), DependsOn: dependency.GetDependsOn()})
	}
	return json.MarshalIndent(document, "", "  ")
}

func toJSONComponent(component *agentv0.Component) jsonComponent {
	value := jsonComponent{
		Type:    cycloneDXType(component.GetType()),
		BomRef:  component.GetBomRef(),
		Group:   component.GetGroup(),
		Name:    component.GetName(),
		Version: component.GetVersion(),
		Purl:    component.GetPurl(),
	}
	for _, hash := range component.GetHashes() {
		value.Hashes = append(value.Hashes, jsonHash{Algorithm: hash.GetAlgorithm(), Content: hash.GetContent()})
	}
	for _, license := range component.GetLicenses() {
		entry := jsonLicense{}
		if isLicenseExpression(license) {
			entry.Expression = license
		} else {
			entry.License = &jsonLicenseChoice{ID: license}
		}
		value.Licenses = append(value.Licenses, entry)
	}
	return value
}

func isLicenseExpression(value string) bool {
	return strings.Contains(value, " OR ") || strings.Contains(value, " AND ") || strings.Contains(value, " WITH ") || strings.ContainsAny(value, "()")
}

func cycloneDXType(componentType agentv0.ComponentType) string {
	switch componentType {
	case agentv0.ComponentType_APPLICATION:
		return "application"
	case agentv0.ComponentType_FRAMEWORK:
		return "framework"
	case agentv0.ComponentType_MODULE:
		return "application"
	case agentv0.ComponentType_CONTAINER:
		return "container"
	default:
		return "library"
	}
}
