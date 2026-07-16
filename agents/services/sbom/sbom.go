// Package sbom produces deterministic, typed CycloneDX inventories for
// service-agent Builder.SBOM implementations.
package sbom

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

// ErrUnsupported means the ecosystem has no authoritative lockfile or
// resolver available. Callers must surface this as UNSUPPORTED, never CLEAN.
var ErrUnsupported = errors.New("authoritative SBOM generation is unsupported")

// Result is the common output returned by ecosystem-specific generators.
type Result struct {
	Bom      *agentv0.Bom
	Tool     string
	Language string
	SHA256   string
}

func finish(root *agentv0.Component, components []*agentv0.Component, dependencies []*agentv0.Dependency, tool, language string) (*Result, error) {
	sort.Slice(components, func(i, j int) bool { return components[i].GetBomRef() < components[j].GetBomRef() })
	for _, dependency := range dependencies {
		sort.Strings(dependency.DependsOn)
		dependency.DependsOn = compact(dependency.DependsOn)
	}
	sort.Slice(dependencies, func(i, j int) bool { return dependencies[i].GetRef() < dependencies[j].GetRef() })

	seed := root.GetBomRef()
	for _, component := range components {
		seed += "\n" + component.GetBomRef()
	}
	for _, dependency := range dependencies {
		seed += "\n" + dependency.GetRef() + "=" + strings.Join(dependency.GetDependsOn(), ",")
	}

	bom := &agentv0.Bom{
		BomFormat:    "CycloneDX",
		SpecVersion:  "1.5",
		SerialNumber: "urn:uuid:" + uuid.NewSHA1(uuid.NameSpaceURL, []byte(seed)).String(),
		Version:      1,
		Components:   components,
		Dependencies: dependencies,
		Metadata: &agentv0.Metadata{
			Component: root,
			Tools: []*agentv0.Tool{{
				Vendor:  "codefly.dev",
				Name:    tool,
				Version: "1",
			}},
		},
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(bom)
	if err != nil {
		return nil, err
	}
	digest := sha256.Sum256(payload)
	return &Result{Bom: bom, Tool: tool, Language: language, SHA256: hex.EncodeToString(digest[:])}, nil
}

func compact(values []string) []string {
	if len(values) < 2 {
		return values
	}
	out := values[:1]
	for _, value := range values[1:] {
		if value != out[len(out)-1] {
			out = append(out, value)
		}
	}
	return out
}

func splitName(value string) (group, name string) {
	if i := strings.LastIndex(value, "/"); i >= 0 {
		return value[:i], value[i+1:]
	}
	return "", value
}

func componentRef(component *agentv0.Component) string {
	if component.GetBomRef() != "" {
		return component.GetBomRef()
	}
	if component.GetPurl() != "" {
		return component.GetPurl()
	}
	return component.GetName() + "@" + component.GetVersion()
}
