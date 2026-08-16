// Package manifest defines the solution executor manifest: the schema that
// describes a codefly:solution plugin's identity, the services it scaffolds,
// the APIs and events it exposes and consumes, its UI extensions, its needs,
// its permissions, and the lifecycle operations it implements. The solution
// spec the executor operates on stays codefly-agnostic and is not modeled here.
package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"slices"
	"sort"

	"github.com/Masterminds/semver/v3"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
	"gopkg.in/yaml.v3"
)

const (
	FileName          = "solution.codefly.yaml"
	SchemaVersionV0   = "codefly.solution-manifest/v0"
	ProtocolVersionV0 = "codefly.solution/v0"
)

var idPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)

// Manifest describes a codefly:solution executor.
type Manifest struct {
	SchemaVersion   string          `yaml:"schema_version" json:"schema_version"`
	ProtocolVersion string          `yaml:"protocol_version" json:"protocol_version"`
	Agent           resources.Agent `yaml:"agent" json:"agent"`
	Services        []Service       `yaml:"services,omitempty" json:"services,omitempty"`
	API             API             `yaml:"api" json:"api"`
	Events          Events          `yaml:"events" json:"events"`
	UI              []UIExtension   `yaml:"ui,omitempty" json:"ui,omitempty"`
	Needs           []Need          `yaml:"needs,omitempty" json:"needs,omitempty"`
	Permissions     []Permission    `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	Lifecycle       Lifecycle       `yaml:"lifecycle" json:"lifecycle"`
}

// Service is one service the solution scaffolds. Descriptor-only solutions
// declare none.
type Service struct {
	ID   string `yaml:"id" json:"id"`
	Name string `yaml:"name" json:"name"`
}

// API declares the APIs a solution exposes and consumes.
type API struct {
	Exposes  []APIDeclaration `yaml:"exposes,omitempty" json:"exposes,omitempty"`
	Consumes []APIDeclaration `yaml:"consumes,omitempty" json:"consumes,omitempty"`
}

// APIDeclaration is one exposed or consumed API endpoint.
type APIDeclaration struct {
	ID       string `yaml:"id" json:"id"`
	Protocol string `yaml:"protocol" json:"protocol"`
}

// Events declares the events a solution emits and consumes.
type Events struct {
	Emits    []EventDeclaration `yaml:"emits,omitempty" json:"emits,omitempty"`
	Consumes []EventDeclaration `yaml:"consumes,omitempty" json:"consumes,omitempty"`
}

// EventDeclaration is one emitted or consumed event.
type EventDeclaration struct {
	ID string `yaml:"id" json:"id"`
}

// UIExtension is one UI extension point contributed by the solution.
type UIExtension struct {
	ID   string `yaml:"id" json:"id"`
	Slot string `yaml:"slot" json:"slot"`
}

// Need is one requirement the solution declares.
type Need struct {
	ID   string `yaml:"id" json:"id"`
	Kind string `yaml:"kind" json:"kind"`
}

// Permission is one permission the solution requires.
type Permission struct {
	ID       string `yaml:"id" json:"id"`
	Action   string `yaml:"action" json:"action"`
	Resource string `yaml:"resource" json:"resource"`
	Reason   string `yaml:"reason" json:"reason"`
	Risk     string `yaml:"risk" json:"risk"`
}

// Lifecycle declares which executor operations the solution implements.
type Lifecycle struct {
	Create  bool `yaml:"create" json:"create"`
	Update  bool `yaml:"update" json:"update"`
	Package bool `yaml:"package" json:"package"`
	Render  bool `yaml:"render" json:"render"`
}

func Load(data []byte) (*Manifest, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode solution manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decode solution manifest: multiple YAML documents are not allowed")
		}
		return nil, fmt.Errorf("decode solution manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (m *Manifest) Validate() error {
	if m == nil {
		return fmt.Errorf("solution manifest is required")
	}
	if m.SchemaVersion != SchemaVersionV0 {
		return fmt.Errorf("unsupported solution manifest schema version %q", m.SchemaVersion)
	}
	if m.ProtocolVersion != ProtocolVersionV0 {
		return fmt.Errorf("unsupported solution protocol version %q", m.ProtocolVersion)
	}
	if m.Agent.Kind != resources.SolutionAgent {
		return fmt.Errorf("agent.kind must be %q", resources.SolutionAgent)
	}
	if m.Agent.Publisher == "" || m.Agent.Name == "" || m.Agent.Version == "" {
		return fmt.Errorf("agent publisher, name, and concrete version are required")
	}
	if m.Agent.Version == "latest" {
		return fmt.Errorf("agent.version must be concrete")
	}
	if _, err := semver.StrictNewVersion(m.Agent.Version); err != nil {
		return fmt.Errorf("agent.version must be semantic: %w", err)
	}

	if err := validateIDs("services", len(m.Services), func(i int) string { return m.Services[i].ID }); err != nil {
		return err
	}
	if err := validateAPIDeclarations("api.exposes", m.API.Exposes); err != nil {
		return err
	}
	if err := validateAPIDeclarations("api.consumes", m.API.Consumes); err != nil {
		return err
	}
	if err := validateIDs("events.emits", len(m.Events.Emits), func(i int) string { return m.Events.Emits[i].ID }); err != nil {
		return err
	}
	if err := validateIDs("events.consumes", len(m.Events.Consumes), func(i int) string { return m.Events.Consumes[i].ID }); err != nil {
		return err
	}
	if err := validateIDs("ui", len(m.UI), func(i int) string { return m.UI[i].ID }); err != nil {
		return err
	}
	if err := validateIDs("needs", len(m.Needs), func(i int) string { return m.Needs[i].ID }); err != nil {
		return err
	}
	if err := validatePermissions(m.Permissions); err != nil {
		return err
	}
	if !m.Lifecycle.Create && !m.Lifecycle.Update && !m.Lifecycle.Package && !m.Lifecycle.Render {
		return fmt.Errorf("lifecycle must declare at least one operation")
	}
	return nil
}

func validateAPIDeclarations(name string, declarations []APIDeclaration) error {
	seen := make(map[string]struct{}, len(declarations))
	for i, declaration := range declarations {
		if !idPattern.MatchString(declaration.ID) {
			return fmt.Errorf("%s[%d].id is invalid", name, i)
		}
		if _, duplicate := seen[declaration.ID]; duplicate {
			return fmt.Errorf("%s[%d].id %q is duplicated", name, i, declaration.ID)
		}
		seen[declaration.ID] = struct{}{}
		switch declaration.Protocol {
		case standards.GRPC, standards.REST, standards.HTTP, standards.TCP, standards.CONNECT:
		default:
			return fmt.Errorf("%s[%d].protocol %q is invalid", name, i, declaration.Protocol)
		}
	}
	return nil
}

func validatePermissions(permissions []Permission) error {
	seen := make(map[string]struct{}, len(permissions))
	for i, permission := range permissions {
		if !idPattern.MatchString(permission.ID) || !idPattern.MatchString(permission.Action) || permission.Reason == "" {
			return fmt.Errorf("permissions[%d] requires a valid id, action, and reason", i)
		}
		if _, duplicate := seen[permission.ID]; duplicate {
			return fmt.Errorf("permissions[%d].id %q is duplicated", i, permission.ID)
		}
		seen[permission.ID] = struct{}{}
		switch permission.Risk {
		case policy.RiskLevelLow, policy.RiskLevelMedium, policy.RiskLevelHigh, policy.RiskLevelCritical:
		default:
			return fmt.Errorf("permissions[%d].risk is invalid", i)
		}
	}
	return nil
}

func validateIDs(name string, count int, id func(int) string) error {
	seen := make(map[string]struct{}, count)
	for i := 0; i < count; i++ {
		value := id(i)
		if !idPattern.MatchString(value) {
			return fmt.Errorf("%s[%d].id is invalid", name, i)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%s[%d].id %q is duplicated", name, i, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

// CanonicalBytes returns a deterministic JSON encoding of the manifest,
// independent of declaration order, suitable for OCI packaging digests.
func (m *Manifest) CanonicalBytes() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	normalized := *m
	normalized.Services = append([]Service(nil), m.Services...)
	sort.Slice(normalized.Services, func(i, j int) bool { return normalized.Services[i].ID < normalized.Services[j].ID })
	normalized.API.Exposes = sortedAPIDeclarations(m.API.Exposes)
	normalized.API.Consumes = sortedAPIDeclarations(m.API.Consumes)
	normalized.Events.Emits = sortedEventDeclarations(m.Events.Emits)
	normalized.Events.Consumes = sortedEventDeclarations(m.Events.Consumes)
	normalized.UI = append([]UIExtension(nil), m.UI...)
	sort.Slice(normalized.UI, func(i, j int) bool { return normalized.UI[i].ID < normalized.UI[j].ID })
	normalized.Needs = append([]Need(nil), m.Needs...)
	sort.Slice(normalized.Needs, func(i, j int) bool { return normalized.Needs[i].ID < normalized.Needs[j].ID })
	normalized.Permissions = append([]Permission(nil), m.Permissions...)
	sort.Slice(normalized.Permissions, func(i, j int) bool { return normalized.Permissions[i].ID < normalized.Permissions[j].ID })
	return json.Marshal(normalized)
}

func (m *Manifest) Digest() (string, error) {
	canonical, err := m.CanonicalBytes()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func sortedAPIDeclarations(values []APIDeclaration) []APIDeclaration {
	out := append([]APIDeclaration(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortedEventDeclarations(values []EventDeclaration) []EventDeclaration {
	out := append([]EventDeclaration(nil), values...)
	slices.SortFunc(out, func(a, b EventDeclaration) int {
		switch {
		case a.ID < b.ID:
			return -1
		case a.ID > b.ID:
			return 1
		default:
			return 0
		}
	})
	return out
}
