package manifest_test

import (
	"strings"
	"testing"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/stretchr/testify/require"
)

func TestManifestLoadValidateAndCanonicalDigest(t *testing.T) {
	first, err := manifest.Load([]byte(validManifest))
	require.NoError(t, err)
	second, err := manifest.Load([]byte(strings.Replace(validManifest,
		"allowed_body_fields: [name, enabled]", "allowed_body_fields: [enabled, name]", 1)))
	require.NoError(t, err)

	firstBytes, err := first.CanonicalBytes()
	require.NoError(t, err)
	secondBytes, err := second.CanonicalBytes()
	require.NoError(t, err)
	require.Equal(t, firstBytes, secondBytes)

	firstDigest, err := first.Digest()
	require.NoError(t, err)
	secondDigest, err := second.Digest()
	require.NoError(t, err)
	require.Equal(t, firstDigest, secondDigest)
}

func TestManifestRejectsUnknownFieldAndBroadProductionMutation(t *testing.T) {
	_, err := manifest.Load([]byte(validManifest + "\nunknown_field: true\n"))
	require.ErrorContains(t, err, "field unknown_field not found")

	broad := strings.Replace(validManifest,
		`resource: "provider:fixture/${workspace}/${environment}/${binding}/account"`,
		`resource: "provider:fixture/*"`, 1)
	_, err = manifest.Load([]byte(broad))
	require.ErrorContains(t, err, "production mutation")
}

func TestManifestRequestDescriptorBindsPathAndOwnershipFields(t *testing.T) {
	unboundPath := strings.Replace(validManifest, "remote_id_parameters: [account_id]", "remote_id_parameters: []", 1)
	_, err := manifest.Load([]byte(unboundPath))
	require.ErrorContains(t, err, "bind every path placeholder")

	unsupportedPath := strings.Replace(validManifest, "{account_id}", "{AccountID}", 1)
	_, err = manifest.Load([]byte(unsupportedPath))
	require.ErrorContains(t, err, "unsupported placeholder")

	unboundOwnership := strings.Replace(validManifest, "ownership_body_fields: [name]", "ownership_body_fields: [owner]", 1)
	_, err = manifest.Load([]byte(unboundOwnership))
	require.ErrorContains(t, err, "undeclared body field")
}

func TestManifestRequestDescriptorBindsPermissionCeiling(t *testing.T) {
	unknown := strings.Replace(validManifest, "permissions: [account-observe]", "permissions: [missing]", 1)
	_, err := manifest.Load([]byte(unknown))
	require.ErrorContains(t, err, "unknown permission")

	wrongResource := strings.Replace(validManifest, "resource_type: account\n      reason: Observe", "resource_type: other\n      reason: Observe", 1)
	_, err = manifest.Load([]byte(wrongResource))
	require.ErrorContains(t, err, "unknown resource_type")

	uncoveredPurpose := strings.Replace(validManifest,
		"risk: high\n      credential_purpose: management",
		"risk: high\n      credential_purpose: runtime", 1)
	uncoveredPurpose = strings.Replace(uncoveredPurpose, "permissions: [account-observe]", "permissions: [account-create]", 1)
	_, err = manifest.Load([]byte(uncoveredPurpose))
	require.ErrorContains(t, err, "undeclared credential purpose")
}

func TestRuntimeCatalogMustBeManifestSubsetAndDescriptorBound(t *testing.T) {
	providerManifest, err := manifest.Load([]byte(validManifest))
	require.NoError(t, err)
	requestDigest, err := manifest.RequestDescriptorDigest(providerManifest.Requests[0])
	require.NoError(t, err)
	catalog := &manifest.Catalog{
		SchemaVersion:       manifest.SchemaVersionV0,
		ProtocolVersion:     manifest.ProtocolVersionV0,
		StateSchemaVersions: []uint32{1},
		Requests:            []manifest.CatalogRequest{{ID: providerManifest.Requests[0].ID, Digest: requestDigest}},
		ResourceTypes:       []manifest.CatalogResource{{ID: "account", Actions: []string{"observe"}}},
		ProjectionContracts: []string{"codefly.dev/configuration/billing@1"},
		DiagnosticCodes:     []string{"provider.fixture.invalid-input"},
	}
	require.NoError(t, providerManifest.ValidateCatalog(catalog))
	catalogDigest, err := catalog.Digest()
	require.NoError(t, err)
	runtime := &providerv0.RuntimeCatalog{
		ProtocolVersion: manifest.ProtocolVersionV0, ManifestSchemaVersion: manifest.SchemaVersionV0,
		StateSchemaVersions: []uint32{1},
		Requests: []*providerv0.RuntimeCatalogRequest{{
			Id: providerManifest.Requests[0].ID, Digest: requestDigest,
		}},
		ResourceTypes:       []*providerv0.RuntimeCatalogResource{{Id: "account", Actions: []string{"observe"}}},
		ProjectionContracts: []string{"codefly.dev/configuration/billing@1"},
		DiagnosticCodes:     []string{"provider.fixture.invalid-input"},
		Digest:              catalogDigest,
	}
	_, err = providerManifest.AdmitRuntimeCatalog(runtime)
	require.NoError(t, err)
	runtime.Digest = "sha256:" + strings.Repeat("0", 64)
	_, err = providerManifest.AdmitRuntimeCatalog(runtime)
	require.ErrorContains(t, err, "catalog digest mismatch")

	tampered := *catalog
	tampered.Requests = []manifest.CatalogRequest{{ID: providerManifest.Requests[0].ID, Digest: "sha256:" + strings.Repeat("0", 64)}}
	require.ErrorContains(t, providerManifest.ValidateCatalog(&tampered), "digest mismatch")

	outside := *catalog
	outside.ResourceTypes = []manifest.CatalogResource{{ID: "account", Actions: []string{"unknown"}}}
	require.ErrorContains(t, providerManifest.ValidateCatalog(&outside), "exceeds packaged manifest")
}

func TestSelectorV1AcceptsOnlyBoundedObjectIndexAndArrayWildcard(t *testing.T) {
	tokens, err := manifest.ParseSelector(manifest.Selector{Version: "v1", Path: "$.data.items[0].secret"})
	require.NoError(t, err)
	require.Len(t, tokens, 4)
	_, err = manifest.ParseSelector(manifest.Selector{Version: "v1", Path: "$.data.items[*].id"})
	require.NoError(t, err)

	for _, path := range []string{
		"$..secret",
		"$.data.*",
		"$.*",
		"$.items[-1]",
		"$.items[01]",
		"$['secret']",
		"$.items[**]",
	} {
		t.Run(path, func(t *testing.T) {
			_, err := manifest.ParseSelector(manifest.Selector{Version: "v1", Path: path})
			require.Error(t, err)
		})
	}
	_, err = manifest.ParseSelector(manifest.Selector{Version: "v2", Path: "$.data"})
	require.ErrorContains(t, err, "unsupported selector version")
}

const validManifest = `
schema_version: codefly.provider-manifest/v0
protocol_version: codefly.provider/v0
state_schema_versions: [1]
agent:
  kind: codefly:provider
  publisher: codefly.dev
  name: fixture
  version: 1.2.3
default_deletion_policy: retain
permissions:
  required:
    - id: account-create
      action: account.manage
      resource: "provider:fixture/${workspace}/${environment}/${binding}/account"
      resource_type: account
      reason: Reconcile the declared account.
      risk: high
      credential_purpose: management
  optional:
    - id: account-observe
      action: account.observe
      resource: "provider:fixture/${workspace}/${environment}/${binding}/account"
      resource_type: account
      reason: Observe the declared account.
      risk: low
      credential_purpose: management
resource_types:
  - id: account
    actions: [create, update, replace, delete, import, manual, blocked, no-op, project-output, observe]
    import_identity: [account_id]
    supports_replace: true
    supports_delete: true
requests:
  - id: account.observe
    permissions: [account-observe]
    resource_type: account
    action: observe
    origin_rule: api
    operation: observe
    method: GET
    path_template: /v1/accounts/{account_id}
    remote_id_parameters: [account_id]
    allowed_query_fields: [expand]
    request_byte_budget: 4096
    response_byte_budget: 65536
    read_only: true
    response_schema: account
    credential_purposes: [management]
  - id: account.create
    permissions: [account-create]
    resource_type: account
    action: create
    origin_rule: api
    operation: create
    method: POST
    path_template: /v1/accounts
    remote_id_parameters: []
    allowed_body_fields: [name, enabled]
    ownership_body_fields: [name]
    request_byte_budget: 8192
    response_byte_budget: 65536
    read_only: false
    response_schema: account
    credential_purposes: [management]
origin_rules:
  - id: api
    defaults: [https://api.example.com]
    schemes: [https]
    host_patterns: [api.example.com, "*.api.example.com"]
    ports: [443]
    binding_override: within-rule
    private_network_classes: [public]
credential_purposes:
  - id: management
    minimum_scope: Manage only the bound account.
    permitted_consumer: management
  - id: runtime
    minimum_scope: Use the bound runtime account.
    permitted_consumer: runtime
response_schemas:
  - id: account
    fields:
      - selector: {version: v1, path: "$.id"}
        disposition: FORWARD_SAFE
      - selector: {version: v1, path: "$.secret"}
        disposition: CAPTURE_TO_SINK
        purpose: runtime
      - selector: {version: v1, path: "$.metadata.internal"}
        disposition: SUPPRESS_REPORT_PRESENCE
projections:
  - id: billing
    contract: codefly.dev/configuration/billing@1
sandbox:
  network: deny
state:
  schema_versions: [1]
  import_identity: true
  replace: true
  delete: true
  stepwise_upgrade: true
diagnostic_namespace: provider.fixture.
`

func FuzzParseSelectorNeverPanics(f *testing.F) {
	for _, seed := range []string{"$", "$.data", "$.items[0]", "$.items[*].id", "$..secret", "$['x']", ""} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, path string) {
		tokens, err := manifest.ParseSelector(manifest.Selector{Version: "v1", Path: path})
		if err == nil {
			require.NoError(t, (manifest.Selector{Version: "v1", Path: path}).Validate())
			_ = tokens
		}
	})
}
