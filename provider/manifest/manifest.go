package manifest

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/Masterminds/semver/v3"
	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/resources"
	"gopkg.in/yaml.v3"
)

const (
	FileName              = "provider.codefly.yaml"
	SchemaVersionV0       = "codefly.provider-manifest/v0"
	ProtocolVersionV0     = "codefly.provider/v0"
	StateSchemaVersionV1  = uint32(1)
	DefaultDeletionRetain = "retain"
	SelectorVersionV1     = "v1"
)

var (
	idPattern           = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[._-][a-z0-9]+)*$`)
	pathParameter       = regexp.MustCompile(`\{([a-z][a-z0-9_-]*)\}`)
	diagnosticNSPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(?:\.[a-z][a-z0-9-]*)+\.$`)
)

type Manifest struct {
	SchemaVersion         string                 `yaml:"schema_version" json:"schema_version"`
	ProtocolVersion       string                 `yaml:"protocol_version" json:"protocol_version"`
	StateSchemaVersions   []uint32               `yaml:"state_schema_versions" json:"state_schema_versions"`
	Agent                 resources.Agent        `yaml:"agent" json:"agent"`
	DefaultDeletionPolicy string                 `yaml:"default_deletion_policy" json:"default_deletion_policy"`
	Permissions           PermissionDeclarations `yaml:"permissions" json:"permissions"`
	ResourceTypes         []ResourceType         `yaml:"resource_types" json:"resource_types"`
	Requests              []RequestDescriptor    `yaml:"requests" json:"requests"`
	OriginRules           []OriginRule           `yaml:"origin_rules" json:"origin_rules"`
	CredentialPurposes    []CredentialPurpose    `yaml:"credential_purposes" json:"credential_purposes"`
	ResponseSchemas       []ResponseSchema       `yaml:"response_schemas" json:"response_schemas"`
	Projections           []Projection           `yaml:"projections" json:"projections"`
	Sandbox               policy.SandboxPolicy   `yaml:"sandbox" json:"sandbox"`
	State                 StateCapabilities      `yaml:"state" json:"state"`
	DiagnosticNamespace   string                 `yaml:"diagnostic_namespace" json:"diagnostic_namespace"`
}

type PermissionDeclarations struct {
	Required []Permission `yaml:"required" json:"required"`
	Optional []Permission `yaml:"optional,omitempty" json:"optional,omitempty"`
}

type Permission struct {
	ID                string `yaml:"id" json:"id"`
	Action            string `yaml:"action" json:"action"`
	Resource          string `yaml:"resource" json:"resource"`
	ResourceType      string `yaml:"resource_type" json:"resource_type"`
	Reason            string `yaml:"reason" json:"reason"`
	Risk              string `yaml:"risk" json:"risk"`
	CredentialPurpose string `yaml:"credential_purpose,omitempty" json:"credential_purpose,omitempty"`
}

type ResourceType struct {
	ID              string   `yaml:"id" json:"id"`
	Actions         []string `yaml:"actions" json:"actions"`
	ImportIdentity  []string `yaml:"import_identity,omitempty" json:"import_identity,omitempty"`
	SupportsReplace bool     `yaml:"supports_replace,omitempty" json:"supports_replace,omitempty"`
	SupportsDelete  bool     `yaml:"supports_delete,omitempty" json:"supports_delete,omitempty"`
}

type RequestDescriptor struct {
	ID                  string   `yaml:"id" json:"id"`
	Permissions         []string `yaml:"permissions" json:"permissions"`
	ResourceType        string   `yaml:"resource_type" json:"resource_type"`
	Action              string   `yaml:"action" json:"action"`
	OriginRule          string   `yaml:"origin_rule" json:"origin_rule"`
	Operation           string   `yaml:"operation" json:"operation"`
	Method              string   `yaml:"method" json:"method"`
	PathTemplate        string   `yaml:"path_template" json:"path_template"`
	RemoteIDParameters  []string `yaml:"remote_id_parameters,omitempty" json:"remote_id_parameters,omitempty"`
	AllowedQueryFields  []string `yaml:"allowed_query_fields,omitempty" json:"allowed_query_fields,omitempty"`
	AllowedBodyFields   []string `yaml:"allowed_body_fields,omitempty" json:"allowed_body_fields,omitempty"`
	OwnershipBodyFields []string `yaml:"ownership_body_fields,omitempty" json:"ownership_body_fields,omitempty"`
	RequestByteBudget   uint64   `yaml:"request_byte_budget" json:"request_byte_budget"`
	ResponseByteBudget  uint64   `yaml:"response_byte_budget" json:"response_byte_budget"`
	ReadOnly            bool     `yaml:"read_only" json:"read_only"`
	ResponseSchema      string   `yaml:"response_schema" json:"response_schema"`
	CredentialPurposes  []string `yaml:"credential_purposes,omitempty" json:"credential_purposes,omitempty"`
}

type OriginRule struct {
	ID                    string   `yaml:"id" json:"id"`
	Defaults              []string `yaml:"defaults" json:"defaults"`
	Schemes               []string `yaml:"schemes" json:"schemes"`
	HostPatterns          []string `yaml:"host_patterns" json:"host_patterns"`
	Ports                 []uint32 `yaml:"ports" json:"ports"`
	BindingOverride       string   `yaml:"binding_override" json:"binding_override"`
	PrivateNetworkClasses []string `yaml:"private_network_classes" json:"private_network_classes"`
}

type CredentialPurpose struct {
	ID                string `yaml:"id" json:"id"`
	MinimumScope      string `yaml:"minimum_scope" json:"minimum_scope"`
	PermittedConsumer string `yaml:"permitted_consumer" json:"permitted_consumer"`
}

type ResponseDisposition string

const (
	ResponseForwardSafe      ResponseDisposition = "FORWARD_SAFE"
	ResponseSuppressPresence ResponseDisposition = "SUPPRESS_REPORT_PRESENCE"
	ResponseCaptureToSink    ResponseDisposition = "CAPTURE_TO_SINK"
)

type ResponseSchema struct {
	ID     string          `yaml:"id" json:"id"`
	Fields []ResponseField `yaml:"fields" json:"fields"`
}

type ResponseField struct {
	Selector    Selector            `yaml:"selector" json:"selector"`
	Disposition ResponseDisposition `yaml:"disposition" json:"disposition"`
	Purpose     string              `yaml:"purpose,omitempty" json:"purpose,omitempty"`
}

type Selector struct {
	Version string `yaml:"version" json:"version"`
	Path    string `yaml:"path" json:"path"`
}

type Projection struct {
	ID       string `yaml:"id" json:"id"`
	Contract string `yaml:"contract" json:"contract"`
}

type StateCapabilities struct {
	SchemaVersions  []uint32 `yaml:"schema_versions" json:"schema_versions"`
	ImportIdentity  bool     `yaml:"import_identity" json:"import_identity"`
	Replace         bool     `yaml:"replace" json:"replace"`
	Delete          bool     `yaml:"delete" json:"delete"`
	StepwiseUpgrade bool     `yaml:"stepwise_upgrade" json:"stepwise_upgrade"`
}

type Catalog struct {
	SchemaVersion       string            `json:"schema_version"`
	ProtocolVersion     string            `json:"protocol_version"`
	StateSchemaVersions []uint32          `json:"state_schema_versions"`
	Requests            []CatalogRequest  `json:"requests"`
	ResourceTypes       []CatalogResource `json:"resource_types"`
	ProjectionContracts []string          `json:"projection_contracts"`
	DiagnosticCodes     []string          `json:"diagnostic_codes"`
}

type CatalogRequest struct {
	ID     string `json:"id"`
	Digest string `json:"digest"`
}

type CatalogResource struct {
	ID      string   `json:"id"`
	Actions []string `json:"actions"`
}

func Load(data []byte) (*Manifest, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var manifest Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode provider manifest: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decode provider manifest: multiple YAML documents are not allowed")
		}
		return nil, fmt.Errorf("decode provider manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	return &manifest, nil
}

func (m *Manifest) Validate() error {
	if m == nil {
		return fmt.Errorf("provider manifest is required")
	}
	if m.SchemaVersion != SchemaVersionV0 {
		return fmt.Errorf("unsupported provider manifest schema version %q", m.SchemaVersion)
	}
	if m.ProtocolVersion != ProtocolVersionV0 {
		return fmt.Errorf("unsupported provider protocol version %q", m.ProtocolVersion)
	}
	if !slices.Equal(m.StateSchemaVersions, []uint32{StateSchemaVersionV1}) {
		return fmt.Errorf("state_schema_versions must be [1]")
	}
	if m.Agent.Kind != resources.ProviderAgent {
		return fmt.Errorf("agent.kind must be %q", resources.ProviderAgent)
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
	if m.DefaultDeletionPolicy != DefaultDeletionRetain {
		return fmt.Errorf("default_deletion_policy must be %q", DefaultDeletionRetain)
	}
	if err := m.Sandbox.Validate(); err != nil {
		return fmt.Errorf("sandbox: %w", err)
	}
	if m.Sandbox.Network == policy.NetworkOpen {
		return fmt.Errorf("sandbox.network cannot grant direct provider network access")
	}
	if len(m.Sandbox.UnixSockets) != 0 {
		return fmt.Errorf("sandbox.unix_sockets cannot grant provider-owned host sockets")
	}
	if !diagnosticNSPattern.MatchString(m.DiagnosticNamespace) {
		return fmt.Errorf("diagnostic_namespace must be a dotted namespace ending in a dot")
	}

	resourceTypes := make(map[string]ResourceType, len(m.ResourceTypes))
	for i, resourceType := range m.ResourceTypes {
		if !idPattern.MatchString(resourceType.ID) {
			return fmt.Errorf("resource_types[%d].id is invalid", i)
		}
		if _, duplicate := resourceTypes[resourceType.ID]; duplicate {
			return fmt.Errorf("resource_types[%d].id %q is duplicated", i, resourceType.ID)
		}
		if err := validateStringSet(fmt.Sprintf("resource_types[%d].actions", i), resourceType.Actions, validAction); err != nil {
			return err
		}
		if slices.Contains(resourceType.Actions, "replace") != resourceType.SupportsReplace {
			return fmt.Errorf("resource_types[%d] replace action and supports_replace must agree", i)
		}
		if slices.Contains(resourceType.Actions, "delete") != resourceType.SupportsDelete {
			return fmt.Errorf("resource_types[%d] delete action and supports_delete must agree", i)
		}
		resourceTypes[resourceType.ID] = resourceType
	}
	if len(resourceTypes) == 0 {
		return fmt.Errorf("at least one resource_type is required")
	}

	originRules := make(map[string]OriginRule, len(m.OriginRules))
	for i, rule := range m.OriginRules {
		if err := validateOriginRule(i, rule); err != nil {
			return err
		}
		if _, duplicate := originRules[rule.ID]; duplicate {
			return fmt.Errorf("origin_rules[%d].id %q is duplicated", i, rule.ID)
		}
		originRules[rule.ID] = rule
	}

	purposes := make(map[string]CredentialPurpose, len(m.CredentialPurposes))
	for i, purpose := range m.CredentialPurposes {
		if !idPattern.MatchString(purpose.ID) || strings.TrimSpace(purpose.MinimumScope) == "" {
			return fmt.Errorf("credential_purposes[%d] requires a valid id and minimum_scope", i)
		}
		switch purpose.PermittedConsumer {
		case "management", "runtime", "build", "webhook-verification":
		default:
			return fmt.Errorf("credential_purposes[%d].permitted_consumer is invalid", i)
		}
		if _, duplicate := purposes[purpose.ID]; duplicate {
			return fmt.Errorf("credential_purposes[%d].id %q is duplicated", i, purpose.ID)
		}
		purposes[purpose.ID] = purpose
	}

	permissions, err := validatePermissions(m.Permissions, resourceTypes, purposes)
	if err != nil {
		return err
	}

	responseSchemas := make(map[string]ResponseSchema, len(m.ResponseSchemas))
	for i, schema := range m.ResponseSchemas {
		if !idPattern.MatchString(schema.ID) || len(schema.Fields) == 0 {
			return fmt.Errorf("response_schemas[%d] requires a valid id and fields", i)
		}
		if _, duplicate := responseSchemas[schema.ID]; duplicate {
			return fmt.Errorf("response_schemas[%d].id %q is duplicated", i, schema.ID)
		}
		seen := make(map[string]struct{}, len(schema.Fields))
		for j, field := range schema.Fields {
			if err := field.Selector.Validate(); err != nil {
				return fmt.Errorf("response_schemas[%d].fields[%d]: %w", i, j, err)
			}
			key := field.Selector.Version + "\x00" + field.Selector.Path
			if _, duplicate := seen[key]; duplicate {
				return fmt.Errorf("response_schemas[%d].fields[%d] duplicates selector %q", i, j, field.Selector.Path)
			}
			seen[key] = struct{}{}
			switch field.Disposition {
			case ResponseForwardSafe, ResponseSuppressPresence:
				if field.Purpose != "" {
					return fmt.Errorf("response_schemas[%d].fields[%d].purpose is only valid for capture", i, j)
				}
			case ResponseCaptureToSink:
				if _, ok := purposes[field.Purpose]; !ok {
					return fmt.Errorf("response_schemas[%d].fields[%d] references unknown credential purpose %q", i, j, field.Purpose)
				}
			default:
				return fmt.Errorf("response_schemas[%d].fields[%d].disposition is invalid", i, j)
			}
		}
		responseSchemas[schema.ID] = schema
	}

	requests := make(map[string]RequestDescriptor, len(m.Requests))
	mutationPermissions := make(map[string]struct{})
	for i, descriptor := range m.Requests {
		if err := validateRequestDescriptor(i, descriptor, resourceTypes, originRules, purposes, responseSchemas, permissions); err != nil {
			return err
		}
		if _, duplicate := requests[descriptor.ID]; duplicate {
			return fmt.Errorf("requests[%d].id %q is duplicated", i, descriptor.ID)
		}
		requests[descriptor.ID] = descriptor
		if !descriptor.ReadOnly {
			for _, permissionID := range descriptor.Permissions {
				mutationPermissions[permissionID] = struct{}{}
			}
		}
	}

	projectionContracts := make(map[string]struct{}, len(m.Projections))
	for i, projection := range m.Projections {
		if !idPattern.MatchString(projection.ID) {
			return fmt.Errorf("projections[%d].id is invalid", i)
		}
		if !versionedContract(projection.Contract) {
			return fmt.Errorf("projections[%d].contract must be versioned", i)
		}
		if _, duplicate := projectionContracts[projection.Contract]; duplicate {
			return fmt.Errorf("projections[%d].contract %q is duplicated", i, projection.Contract)
		}
		projectionContracts[projection.Contract] = struct{}{}
	}

	if !slices.Equal(m.State.SchemaVersions, m.StateSchemaVersions) {
		return fmt.Errorf("state.schema_versions must match state_schema_versions")
	}
	if err := validateProductionMutationPermissions(m.Agent.Name, permissions, mutationPermissions); err != nil {
		return err
	}
	return nil
}

func validatePermissions(declarations PermissionDeclarations, resourceTypes map[string]ResourceType, purposes map[string]CredentialPurpose) (map[string]Permission, error) {
	if len(declarations.Required) == 0 {
		return nil, fmt.Errorf("permissions.required must not be empty")
	}
	permissions := make(map[string]Permission, len(declarations.Required)+len(declarations.Optional))
	seenCeilings := make(map[string]struct{}, len(declarations.Required)+len(declarations.Optional))
	for _, group := range []struct {
		name        string
		permissions []Permission
	}{
		{name: "required", permissions: declarations.Required},
		{name: "optional", permissions: declarations.Optional},
	} {
		for i, permission := range group.permissions {
			if !idPattern.MatchString(permission.ID) || !idPattern.MatchString(permission.Action) || strings.TrimSpace(permission.Reason) == "" {
				return nil, fmt.Errorf("permissions.%s[%d] requires a valid id, action, and reason", group.name, i)
			}
			if _, duplicate := permissions[permission.ID]; duplicate {
				return nil, fmt.Errorf("permissions.%s[%d].id %q is duplicated", group.name, i, permission.ID)
			}
			if _, ok := resourceTypes[permission.ResourceType]; !ok {
				return nil, fmt.Errorf("permissions.%s[%d] references unknown resource_type %q", group.name, i, permission.ResourceType)
			}
			switch permission.Risk {
			case policy.RiskLevelLow, policy.RiskLevelMedium, policy.RiskLevelHigh, policy.RiskLevelCritical:
			default:
				return nil, fmt.Errorf("permissions.%s[%d].risk is invalid", group.name, i)
			}
			if permission.CredentialPurpose != "" {
				if _, ok := purposes[permission.CredentialPurpose]; !ok {
					return nil, fmt.Errorf("permissions.%s[%d] references unknown credential purpose %q", group.name, i, permission.CredentialPurpose)
				}
			}
			key := permission.Action + "\x00" + permission.Resource + "\x00" + permission.CredentialPurpose
			if _, duplicate := seenCeilings[key]; duplicate {
				return nil, fmt.Errorf("permissions.%s[%d] duplicates action/resource/credential purpose", group.name, i)
			}
			seenCeilings[key] = struct{}{}
			permissions[permission.ID] = permission
		}
	}
	return permissions, nil
}

func validateProductionMutationPermissions(provider string, permissions map[string]Permission, mutations map[string]struct{}) error {
	for permissionID := range mutations {
		permission := permissions[permissionID]
		if permission.Resource == "*" || permission.Resource == "provider:"+provider+"/*" {
			return fmt.Errorf("production mutation permission %q cannot use broad resource %q", permissionID, permission.Resource)
		}
		for _, binding := range []string{"${workspace}", "${environment}", "${binding}"} {
			if !strings.Contains(permission.Resource, binding) {
				return fmt.Errorf("production mutation permission %q resource must bind %s", permissionID, binding)
			}
		}
		if !strings.Contains(permission.Resource, permission.ResourceType) && !strings.Contains(permission.Resource, "${resource_type}") {
			return fmt.Errorf("production mutation permission %q resource must bind resource type", permissionID)
		}
	}
	return nil
}

func validateOriginRule(index int, rule OriginRule) error {
	if !idPattern.MatchString(rule.ID) || len(rule.Defaults) == 0 || len(rule.Schemes) == 0 || len(rule.HostPatterns) == 0 || len(rule.Ports) == 0 {
		return fmt.Errorf("origin_rules[%d] requires id, defaults, schemes, host_patterns, and ports", index)
	}
	if err := validateStringSet(fmt.Sprintf("origin_rules[%d].schemes", index), rule.Schemes, func(value string) bool {
		return value == "https" || value == "http"
	}); err != nil {
		return err
	}
	ports := make(map[uint32]struct{}, len(rule.Ports))
	for _, port := range rule.Ports {
		if port == 0 || port > 65535 {
			return fmt.Errorf("origin_rules[%d].ports contains invalid port %d", index, port)
		}
		if _, duplicate := ports[port]; duplicate {
			return fmt.Errorf("origin_rules[%d].ports contains duplicate %d", index, port)
		}
		ports[port] = struct{}{}
	}
	for _, host := range rule.HostPatterns {
		base := strings.TrimPrefix(host, "*.")
		if host == "*" || net.ParseIP(base) != nil || !validHostname(base) {
			return fmt.Errorf("origin_rules[%d].host_patterns contains invalid bounded pattern %q", index, host)
		}
	}
	switch rule.BindingOverride {
	case "deny", "exact", "within-rule":
	default:
		return fmt.Errorf("origin_rules[%d].binding_override is invalid", index)
	}
	if err := validateStringSet(fmt.Sprintf("origin_rules[%d].private_network_classes", index), rule.PrivateNetworkClasses, func(value string) bool {
		return slices.Contains([]string{"public", "loopback", "link-local", "private"}, value)
	}); err != nil {
		return err
	}
	for _, raw := range rule.Defaults {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil {
			return fmt.Errorf("origin_rules[%d].defaults contains invalid origin %q", index, raw)
		}
		if !slices.Contains(rule.Schemes, parsed.Scheme) || !hostMatchesAny(parsed.Hostname(), rule.HostPatterns) {
			return fmt.Errorf("origin_rules[%d].defaults origin %q is outside its scheme/host ceiling", index, raw)
		}
		port := uint32(443)
		if parsed.Scheme == "http" {
			port = 80
		}
		if parsed.Port() != "" {
			n, err := strconv.ParseUint(parsed.Port(), 10, 16)
			if err != nil {
				return fmt.Errorf("origin_rules[%d].defaults contains invalid port", index)
			}
			port = uint32(n)
		}
		if !slices.Contains(rule.Ports, port) {
			return fmt.Errorf("origin_rules[%d].defaults origin %q is outside its port ceiling", index, raw)
		}
	}
	return nil
}

func validateRequestDescriptor(index int, descriptor RequestDescriptor, resourcesByID map[string]ResourceType, origins map[string]OriginRule, purposes map[string]CredentialPurpose, responses map[string]ResponseSchema, permissions map[string]Permission) error {
	if !idPattern.MatchString(descriptor.ID) || !idPattern.MatchString(descriptor.Operation) {
		return fmt.Errorf("requests[%d] requires valid id and operation", index)
	}
	resourceType, ok := resourcesByID[descriptor.ResourceType]
	if !ok || !slices.Contains(resourceType.Actions, descriptor.Action) {
		return fmt.Errorf("requests[%d] references unsupported resource_type/action", index)
	}
	if _, ok := origins[descriptor.OriginRule]; !ok {
		return fmt.Errorf("requests[%d] references unknown origin_rule %q", index, descriptor.OriginRule)
	}
	if _, ok := responses[descriptor.ResponseSchema]; !ok {
		return fmt.Errorf("requests[%d] references unknown response_schema %q", index, descriptor.ResponseSchema)
	}
	switch descriptor.Method {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE":
	default:
		return fmt.Errorf("requests[%d].method is invalid", index)
	}
	if descriptor.ReadOnly != (descriptor.Method == "GET" || descriptor.Method == "HEAD") {
		return fmt.Errorf("requests[%d].read_only must agree with HTTP method", index)
	}
	if descriptor.PathTemplate == "" || descriptor.PathTemplate[0] != '/' || strings.Contains(descriptor.PathTemplate, "..") || strings.ContainsAny(descriptor.PathTemplate, "?#") {
		return fmt.Errorf("requests[%d].path_template is invalid", index)
	}
	bound := pathParameter.FindAllStringSubmatch(descriptor.PathTemplate, -1)
	parameters := make([]string, 0, len(bound))
	for _, match := range bound {
		parameters = append(parameters, match[1])
	}
	if unresolved := pathParameter.ReplaceAllString(descriptor.PathTemplate, ""); strings.ContainsAny(unresolved, "{}") {
		return fmt.Errorf("requests[%d].path_template contains an unsupported placeholder", index)
	}
	sort.Strings(parameters)
	remoteIDs := append([]string(nil), descriptor.RemoteIDParameters...)
	sort.Strings(remoteIDs)
	if !slices.Equal(parameters, remoteIDs) {
		return fmt.Errorf("requests[%d].remote_id_parameters must bind every path placeholder exactly", index)
	}
	for fieldName, fields := range map[string][]string{
		"permissions":           descriptor.Permissions,
		"remote_id_parameters":  descriptor.RemoteIDParameters,
		"allowed_query_fields":  descriptor.AllowedQueryFields,
		"allowed_body_fields":   descriptor.AllowedBodyFields,
		"ownership_body_fields": descriptor.OwnershipBodyFields,
		"credential_purposes":   descriptor.CredentialPurposes,
	} {
		if err := validateStringSet(fmt.Sprintf("requests[%d].%s", index, fieldName), fields, func(value string) bool { return idPattern.MatchString(value) }); err != nil {
			return err
		}
	}
	for _, field := range descriptor.OwnershipBodyFields {
		if !slices.Contains(descriptor.AllowedBodyFields, field) {
			return fmt.Errorf("requests[%d].ownership_body_fields contains undeclared body field %q", index, field)
		}
	}
	for _, purpose := range descriptor.CredentialPurposes {
		if _, ok := purposes[purpose]; !ok {
			return fmt.Errorf("requests[%d] references unknown credential purpose %q", index, purpose)
		}
	}
	coveredPurposes := make(map[string]struct{}, len(descriptor.Permissions))
	for _, permissionID := range descriptor.Permissions {
		permission, ok := permissions[permissionID]
		if !ok {
			return fmt.Errorf("requests[%d] references unknown permission %q", index, permissionID)
		}
		if permission.ResourceType != descriptor.ResourceType {
			return fmt.Errorf("requests[%d] permission %q has a different resource_type", index, permissionID)
		}
		if permission.CredentialPurpose == "" {
			if len(descriptor.CredentialPurposes) != 0 {
				return fmt.Errorf("requests[%d] permission %q does not bind a credential purpose", index, permissionID)
			}
		} else if !slices.Contains(descriptor.CredentialPurposes, permission.CredentialPurpose) {
			return fmt.Errorf("requests[%d] permission %q binds an undeclared credential purpose", index, permissionID)
		}
		coveredPurposes[permission.CredentialPurpose] = struct{}{}
	}
	if len(descriptor.Permissions) == 0 {
		return fmt.Errorf("requests[%d] must bind at least one permission", index)
	}
	if len(descriptor.CredentialPurposes) == 0 {
		if _, covered := coveredPurposes[""]; !covered {
			return fmt.Errorf("requests[%d] permission does not cover its credential-free request", index)
		}
	}
	for _, purpose := range descriptor.CredentialPurposes {
		if _, covered := coveredPurposes[purpose]; !covered {
			return fmt.Errorf("requests[%d] permissions do not cover credential purpose %q", index, purpose)
		}
	}
	if descriptor.RequestByteBudget == 0 || descriptor.ResponseByteBudget == 0 {
		return fmt.Errorf("requests[%d] requires non-zero request and response budgets", index)
	}
	return nil
}

func (m *Manifest) ValidateCatalog(catalog *Catalog) error {
	if err := m.Validate(); err != nil {
		return err
	}
	if catalog == nil {
		return fmt.Errorf("runtime catalog is required")
	}
	if catalog.SchemaVersion != m.SchemaVersion || catalog.ProtocolVersion != m.ProtocolVersion || !slices.Equal(catalog.StateSchemaVersions, m.StateSchemaVersions) {
		return fmt.Errorf("runtime catalog versions are not covered by packaged manifest")
	}
	requests := make(map[string]RequestDescriptor, len(m.Requests))
	for _, descriptor := range m.Requests {
		requests[descriptor.ID] = descriptor
	}
	seenRequests := make(map[string]struct{}, len(catalog.Requests))
	for i, advertised := range catalog.Requests {
		descriptor, ok := requests[advertised.ID]
		if !ok {
			return fmt.Errorf("runtime catalog requests[%d] %q is not in packaged manifest", i, advertised.ID)
		}
		digest, err := RequestDescriptorDigest(descriptor)
		if err != nil {
			return err
		}
		if advertised.Digest != digest {
			return fmt.Errorf("runtime catalog requests[%d] %q digest mismatch", i, advertised.ID)
		}
		if _, duplicate := seenRequests[advertised.ID]; duplicate {
			return fmt.Errorf("runtime catalog requests[%d] %q is duplicated", i, advertised.ID)
		}
		seenRequests[advertised.ID] = struct{}{}
	}
	resourcesByID := make(map[string]ResourceType, len(m.ResourceTypes))
	for _, resourceType := range m.ResourceTypes {
		resourcesByID[resourceType.ID] = resourceType
	}
	seenResources := make(map[string]struct{}, len(catalog.ResourceTypes))
	for i, advertised := range catalog.ResourceTypes {
		ceiling, ok := resourcesByID[advertised.ID]
		if !ok {
			return fmt.Errorf("runtime catalog resource_types[%d] %q is not in packaged manifest", i, advertised.ID)
		}
		if _, duplicate := seenResources[advertised.ID]; duplicate {
			return fmt.Errorf("runtime catalog resource_types[%d] %q is duplicated", i, advertised.ID)
		}
		seenResources[advertised.ID] = struct{}{}
		seenActions := make(map[string]struct{}, len(advertised.Actions))
		for _, action := range advertised.Actions {
			if !slices.Contains(ceiling.Actions, action) {
				return fmt.Errorf("runtime catalog resource_types[%d] action %q exceeds packaged manifest", i, action)
			}
			if _, duplicate := seenActions[action]; duplicate {
				return fmt.Errorf("runtime catalog resource_types[%d] action %q is duplicated", i, action)
			}
			seenActions[action] = struct{}{}
		}
	}
	contracts := make(map[string]struct{}, len(m.Projections))
	for _, projection := range m.Projections {
		contracts[projection.Contract] = struct{}{}
	}
	seenContracts := make(map[string]struct{}, len(catalog.ProjectionContracts))
	for i, contract := range catalog.ProjectionContracts {
		if _, ok := contracts[contract]; !ok {
			return fmt.Errorf("runtime catalog projection_contracts[%d] %q is not in packaged manifest", i, contract)
		}
		if _, duplicate := seenContracts[contract]; duplicate {
			return fmt.Errorf("runtime catalog projection_contracts[%d] %q is duplicated", i, contract)
		}
		seenContracts[contract] = struct{}{}
	}
	seenCodes := make(map[string]struct{}, len(catalog.DiagnosticCodes))
	for i, code := range catalog.DiagnosticCodes {
		if !strings.HasPrefix(code, m.DiagnosticNamespace) || code == m.DiagnosticNamespace {
			return fmt.Errorf("runtime catalog diagnostic_codes[%d] is outside namespace %q", i, m.DiagnosticNamespace)
		}
		if _, duplicate := seenCodes[code]; duplicate {
			return fmt.Errorf("runtime catalog diagnostic_codes[%d] %q is duplicated", i, code)
		}
		seenCodes[code] = struct{}{}
	}
	return nil
}

func (m *Manifest) AdmitRuntimeCatalog(runtime *providerv0.RuntimeCatalog) (*Catalog, error) {
	if runtime == nil {
		return nil, fmt.Errorf("runtime catalog is required")
	}
	catalog := &Catalog{
		SchemaVersion:       runtime.GetManifestSchemaVersion(),
		ProtocolVersion:     runtime.GetProtocolVersion(),
		StateSchemaVersions: append([]uint32(nil), runtime.GetStateSchemaVersions()...),
		ProjectionContracts: append([]string(nil), runtime.GetProjectionContracts()...),
		DiagnosticCodes:     append([]string(nil), runtime.GetDiagnosticCodes()...),
	}
	for _, request := range runtime.GetRequests() {
		catalog.Requests = append(catalog.Requests, CatalogRequest{ID: request.GetId(), Digest: request.GetDigest()})
	}
	for _, resourceType := range runtime.GetResourceTypes() {
		catalog.ResourceTypes = append(catalog.ResourceTypes, CatalogResource{
			ID: resourceType.GetId(), Actions: append([]string(nil), resourceType.GetActions()...),
		})
	}
	if err := m.ValidateCatalog(catalog); err != nil {
		return nil, err
	}
	computed, err := catalog.Digest()
	if err != nil {
		return nil, err
	}
	if runtime.GetDigest() != computed {
		return nil, fmt.Errorf("runtime catalog digest mismatch")
	}
	return catalog, nil
}

func (m *Manifest) CanonicalBytes() ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	normalized := *m
	normalized.StateSchemaVersions = sortedUint32(m.StateSchemaVersions)
	normalized.Permissions.Required = sortedPermissions(m.Permissions.Required)
	normalized.Permissions.Optional = sortedPermissions(m.Permissions.Optional)
	normalized.ResourceTypes = append([]ResourceType(nil), m.ResourceTypes...)
	for i := range normalized.ResourceTypes {
		normalized.ResourceTypes[i].Actions = sortedStrings(normalized.ResourceTypes[i].Actions)
		normalized.ResourceTypes[i].ImportIdentity = sortedStrings(normalized.ResourceTypes[i].ImportIdentity)
	}
	sort.Slice(normalized.ResourceTypes, func(i, j int) bool { return normalized.ResourceTypes[i].ID < normalized.ResourceTypes[j].ID })
	normalized.Requests = append([]RequestDescriptor(nil), m.Requests...)
	for i := range normalized.Requests {
		normalizeRequestDescriptor(&normalized.Requests[i])
	}
	sort.Slice(normalized.Requests, func(i, j int) bool { return normalized.Requests[i].ID < normalized.Requests[j].ID })
	normalized.OriginRules = append([]OriginRule(nil), m.OriginRules...)
	for i := range normalized.OriginRules {
		normalized.OriginRules[i].Defaults = sortedStrings(normalized.OriginRules[i].Defaults)
		normalized.OriginRules[i].Schemes = sortedStrings(normalized.OriginRules[i].Schemes)
		normalized.OriginRules[i].HostPatterns = sortedStrings(normalized.OriginRules[i].HostPatterns)
		normalized.OriginRules[i].Ports = sortedUint32(normalized.OriginRules[i].Ports)
		normalized.OriginRules[i].PrivateNetworkClasses = sortedStrings(normalized.OriginRules[i].PrivateNetworkClasses)
	}
	sort.Slice(normalized.OriginRules, func(i, j int) bool { return normalized.OriginRules[i].ID < normalized.OriginRules[j].ID })
	normalized.CredentialPurposes = append([]CredentialPurpose(nil), m.CredentialPurposes...)
	sort.Slice(normalized.CredentialPurposes, func(i, j int) bool { return normalized.CredentialPurposes[i].ID < normalized.CredentialPurposes[j].ID })
	normalized.ResponseSchemas = append([]ResponseSchema(nil), m.ResponseSchemas...)
	for i := range normalized.ResponseSchemas {
		normalized.ResponseSchemas[i].Fields = append([]ResponseField(nil), normalized.ResponseSchemas[i].Fields...)
		sort.Slice(normalized.ResponseSchemas[i].Fields, func(a, b int) bool {
			left, right := normalized.ResponseSchemas[i].Fields[a], normalized.ResponseSchemas[i].Fields[b]
			return left.Selector.Version+"\x00"+left.Selector.Path < right.Selector.Version+"\x00"+right.Selector.Path
		})
	}
	sort.Slice(normalized.ResponseSchemas, func(i, j int) bool { return normalized.ResponseSchemas[i].ID < normalized.ResponseSchemas[j].ID })
	normalized.Projections = append([]Projection(nil), m.Projections...)
	sort.Slice(normalized.Projections, func(i, j int) bool { return normalized.Projections[i].ID < normalized.Projections[j].ID })
	normalized.Sandbox.ReadPaths = sortedStrings(m.Sandbox.ReadPaths)
	normalized.Sandbox.WritePaths = sortedStrings(m.Sandbox.WritePaths)
	normalized.Sandbox.UnixSockets = sortedStrings(m.Sandbox.UnixSockets)
	normalized.State.SchemaVersions = sortedUint32(m.State.SchemaVersions)
	return json.Marshal(normalized)
}

func (m *Manifest) Digest() (string, error) {
	canonical, err := m.CanonicalBytes()
	if err != nil {
		return "", err
	}
	return digest(canonical), nil
}

func (c *Catalog) CanonicalBytes() ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("runtime catalog is required")
	}
	normalized := *c
	normalized.StateSchemaVersions = sortedUint32(c.StateSchemaVersions)
	normalized.Requests = append([]CatalogRequest(nil), c.Requests...)
	sort.Slice(normalized.Requests, func(i, j int) bool { return normalized.Requests[i].ID < normalized.Requests[j].ID })
	normalized.ResourceTypes = append([]CatalogResource(nil), c.ResourceTypes...)
	for i := range normalized.ResourceTypes {
		normalized.ResourceTypes[i].Actions = sortedStrings(normalized.ResourceTypes[i].Actions)
	}
	sort.Slice(normalized.ResourceTypes, func(i, j int) bool { return normalized.ResourceTypes[i].ID < normalized.ResourceTypes[j].ID })
	normalized.ProjectionContracts = sortedStrings(c.ProjectionContracts)
	normalized.DiagnosticCodes = sortedStrings(c.DiagnosticCodes)
	return json.Marshal(normalized)
}

func (c *Catalog) Digest() (string, error) {
	canonical, err := c.CanonicalBytes()
	if err != nil {
		return "", err
	}
	return digest(canonical), nil
}

func RequestDescriptorDigest(descriptor RequestDescriptor) (string, error) {
	normalizeRequestDescriptor(&descriptor)
	data, err := json.Marshal(descriptor)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

func normalizeRequestDescriptor(descriptor *RequestDescriptor) {
	descriptor.Permissions = sortedStrings(descriptor.Permissions)
	descriptor.RemoteIDParameters = sortedStrings(descriptor.RemoteIDParameters)
	descriptor.AllowedQueryFields = sortedStrings(descriptor.AllowedQueryFields)
	descriptor.AllowedBodyFields = sortedStrings(descriptor.AllowedBodyFields)
	descriptor.OwnershipBodyFields = sortedStrings(descriptor.OwnershipBodyFields)
	descriptor.CredentialPurposes = sortedStrings(descriptor.CredentialPurposes)
}

func validateStringSet(name string, values []string, valid func(string) bool) error {
	seen := make(map[string]struct{}, len(values))
	for i, value := range values {
		if !valid(value) {
			return fmt.Errorf("%s[%d] is invalid", name, i)
		}
		if _, duplicate := seen[value]; duplicate {
			return fmt.Errorf("%s[%d] %q is duplicated", name, i, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validAction(value string) bool {
	return slices.Contains([]string{"create", "update", "replace", "delete", "import", "manual", "blocked", "no-op", "project-output", "observe"}, value)
}

func versionedContract(value string) bool {
	at := strings.LastIndexByte(value, '@')
	if at < 1 || at == len(value)-1 {
		return false
	}
	_, err := strconv.ParseUint(value[at+1:], 10, 32)
	return err == nil
}

func validHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 || strings.Contains(host, "..") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func hostMatchesAny(host string, patterns []string) bool {
	for _, pattern := range patterns {
		if host == pattern || (strings.HasPrefix(pattern, "*.") && strings.HasSuffix(host, pattern[1:]) && host != pattern[2:]) {
			return true
		}
	}
	return false
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func sortedUint32(values []uint32) []uint32 {
	out := append([]uint32(nil), values...)
	slices.Sort(out)
	return out
}

func sortedPermissions(values []Permission) []Permission {
	out := append([]Permission(nil), values...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
