// Package manifest defines the solution executor manifest: the schema that
// describes a codefly:solution plugin's identity, the services it scaffolds,
// the APIs and events it exposes and consumes, its UI extensions, its needs,
// its permissions, and the lifecycle operations it implements. The solution
// spec the executor operates on stays codefly-agnostic and is not modeled here.
//
// A consumed API (api.consumes) names the producing endpoint by composition
// identity through the Module, Service, and Endpoint fields — the same names a
// service-dependency uses. Version constrains the producing module package
// version, Services restricts the generated client to a subset of the
// contract's protobuf services, and As names the facade entry-point. The CLI
// derives both the runtime service-dependencies and the aggregated solution SDK
// from api.consumes; a solution does not declare the same dependency twice.
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
	"sort"

	"github.com/Masterminds/semver/v3"
	solutionv0 "github.com/codefly-dev/core/generated/go/codefly/services/solution/v0"
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

	// Consumed-API binding (api.consumes only). Module/Service/Endpoint name
	// the producing endpoint by composition identity — the same names a
	// service-dependency uses — so the CLI can resolve the contract from the
	// composed module package and bind the runtime address. Empty on exposes.
	Module   string `yaml:"module,omitempty" json:"module,omitempty"`
	Service  string `yaml:"service,omitempty" json:"service,omitempty"`
	Endpoint string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	// Version is a semver constraint on the producing module package version
	// (e.g. ">=0.1.0 <0.2.0"). Empty means "whatever the composition lock pins".
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
	// Services restricts the generated client to these protobuf service names
	// (e.g. [AuditService]). Empty means every service of the contract.
	Services []string `yaml:"services,omitempty" json:"services,omitempty"`
	// As is the facade entry-point name (default: the endpoint's package
	// short name, e.g. "accounts").
	As string `yaml:"as,omitempty" json:"as,omitempty"`
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
	for i := range m.Services {
		if m.Services[i].Name == "" {
			return fmt.Errorf("services[%d].name is required", i)
		}
	}
	if err := validateExposedAPIs("api.exposes", m.API.Exposes); err != nil {
		return err
	}
	if err := validateConsumedAPIs("api.consumes", m.API.Consumes); err != nil {
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
	for i := range m.UI {
		if m.UI[i].Slot == "" {
			return fmt.Errorf("ui[%d].slot is required", i)
		}
	}
	if err := validateIDs("needs", len(m.Needs), func(i int) string { return m.Needs[i].ID }); err != nil {
		return err
	}
	for i := range m.Needs {
		if m.Needs[i].Kind == "" {
			return fmt.Errorf("needs[%d].kind is required", i)
		}
	}
	if err := validatePermissions(m.Permissions); err != nil {
		return err
	}
	if !m.Lifecycle.Create && !m.Lifecycle.Update && !m.Lifecycle.Package && !m.Lifecycle.Render {
		return fmt.Errorf("lifecycle must declare at least one operation")
	}
	return nil
}

// compositionNamePattern matches module/service/endpoint identifiers: lowercase
// alphanumeric words joined by single hyphens. This is stricter than
// composition/schema.go's identifier pattern (which also permits '.', '_', and
// '/'); consumed-API bindings deliberately allow only the hyphenated form.
var compositionNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*$`)

// protoServiceNamePattern matches protobuf service names (e.g. AuditService).
var protoServiceNamePattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9]*$`)

func validateAPIDeclarationBase(name string, i int, declaration APIDeclaration, seen map[string]struct{}) error {
	if !idPattern.MatchString(declaration.ID) {
		return fmt.Errorf("%s[%d].id is invalid", name, i)
	}
	if _, duplicate := seen[declaration.ID]; duplicate {
		return fmt.Errorf("%s[%d].id %q is duplicated", name, i, declaration.ID)
	}
	seen[declaration.ID] = struct{}{}
	switch declaration.Protocol {
	case standards.GRPC, standards.REST, standards.HTTP, standards.TCP, standards.CONNECT, standards.MCP:
	default:
		return fmt.Errorf("%s[%d].protocol %q is invalid", name, i, declaration.Protocol)
	}
	return nil
}

func validateExposedAPIs(name string, declarations []APIDeclaration) error {
	seen := make(map[string]struct{}, len(declarations))
	for i, declaration := range declarations {
		if err := validateAPIDeclarationBase(name, i, declaration, seen); err != nil {
			return err
		}
		for _, field := range []struct {
			name string
			set  bool
		}{
			{"module", declaration.Module != ""},
			{"service", declaration.Service != ""},
			{"endpoint", declaration.Endpoint != ""},
			{"version", declaration.Version != ""},
			{"services", len(declaration.Services) > 0},
			{"as", declaration.As != ""},
		} {
			if field.set {
				return fmt.Errorf("%s[%d].%s is not allowed", name, i, field.name)
			}
		}
	}
	return nil
}

func validateConsumedAPIs(name string, declarations []APIDeclaration) error {
	seen := make(map[string]struct{}, len(declarations))
	seenAs := make(map[string]struct{}, len(declarations))
	seenTriples := make(map[string]struct{}, len(declarations))
	for i, declaration := range declarations {
		if err := validateAPIDeclarationBase(name, i, declaration, seen); err != nil {
			return err
		}
		bound := declaration.Module != "" || declaration.Service != "" || declaration.Endpoint != ""
		allBound := declaration.Module != "" && declaration.Service != "" && declaration.Endpoint != ""
		if bound && !allBound {
			return fmt.Errorf("%s[%d] must set module, service, and endpoint together or leave all empty", name, i)
		}
		if allBound {
			for field, value := range map[string]string{
				"module":   declaration.Module,
				"service":  declaration.Service,
				"endpoint": declaration.Endpoint,
			} {
				if !compositionNamePattern.MatchString(value) {
					return fmt.Errorf("%s[%d].%s %q is invalid", name, i, field, value)
				}
			}
			triple := declaration.Module + "/" + declaration.Service + "/" + declaration.Endpoint
			if _, duplicate := seenTriples[triple]; duplicate {
				return fmt.Errorf("%s[%d] (module, service, endpoint) %q is duplicated", name, i, triple)
			}
			seenTriples[triple] = struct{}{}
		}
		if declaration.Version != "" {
			if _, err := semver.NewConstraint(declaration.Version); err != nil {
				return fmt.Errorf("%s[%d].version %q is invalid: %w", name, i, declaration.Version, err)
			}
		}
		seenServices := make(map[string]struct{}, len(declaration.Services))
		for _, service := range declaration.Services {
			if !protoServiceNamePattern.MatchString(service) {
				return fmt.Errorf("%s[%d].services entry %q is invalid", name, i, service)
			}
			if _, duplicate := seenServices[service]; duplicate {
				return fmt.Errorf("%s[%d].services entry %q is duplicated", name, i, service)
			}
			seenServices[service] = struct{}{}
		}
		if declaration.As != "" {
			if !idPattern.MatchString(declaration.As) {
				return fmt.Errorf("%s[%d].as %q is invalid", name, i, declaration.As)
			}
			if _, duplicate := seenAs[declaration.As]; duplicate {
				return fmt.Errorf("%s[%d].as %q is duplicated", name, i, declaration.As)
			}
			seenAs[declaration.As] = struct{}{}
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
		if permission.Resource == "" || permission.Resource == "*" {
			return fmt.Errorf("permissions[%d].resource must be a bounded resource identifier", i)
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

// AdmitInformation binds a runtime GetSolutionInformation advertisement to this
// packaged manifest. The advertised manifest digest must equal this manifest's
// digest, and every lifecycle operation the runtime advertises must have been
// declared in the packaged manifest. A runtime may implement a subset of the
// declared operations, but never one the audited manifest did not declare.
func (m *Manifest) AdmitInformation(info *solutionv0.GetSolutionInformationResponse) error {
	if info == nil {
		return fmt.Errorf("solution information is required")
	}
	digest, err := m.Digest()
	if err != nil {
		return err
	}
	if advertised := info.GetArtifact().GetManifestDigest(); advertised != digest {
		return fmt.Errorf("manifest digest mismatch: advertised %q, packaged %q", advertised, digest)
	}
	capabilities := info.GetCapabilities()
	for _, operation := range []struct {
		name       string
		advertised bool
		declared   bool
	}{
		{"create", capabilities.GetSupportsCreate(), m.Lifecycle.Create},
		{"update", capabilities.GetSupportsUpdate(), m.Lifecycle.Update},
		{"package", capabilities.GetSupportsPackage(), m.Lifecycle.Package},
		{"render", capabilities.GetSupportsRender(), m.Lifecycle.Render},
	} {
		if operation.advertised && !operation.declared {
			return fmt.Errorf("runtime advertises %s but the packaged manifest does not declare it", operation.name)
		}
	}
	return nil
}

func sortedAPIDeclarations(values []APIDeclaration) []APIDeclaration {
	out := append([]APIDeclaration(nil), values...)
	for i := range out {
		if len(out[i].Services) > 0 {
			services := append([]string(nil), out[i].Services...)
			sort.Strings(services)
			out[i].Services = services
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortedEventDeclarations(values []EventDeclaration) []EventDeclaration {
	out := append([]EventDeclaration(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
