package artifact_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codefly-dev/core/provider/artifact"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestProviderArtifactLayoutInstallAndVerification(t *testing.T) {
	root := writeLayout(t, []byte("#!/bin/sh\nexit 0\n"), artifactManifest)
	expected := providerAgent("1.2.3")

	verified, err := artifact.VerifyLayout(root, expected)
	require.NoError(t, err)
	require.Equal(t, expected.Identifier(), verified.Manifest.Agent.Identifier())
	require.NotEmpty(t, verified.Descriptor.ArtifactDigest)

	target := filepath.Join(t.TempDir(), "providers", "codefly.dev", "fixture__1.2.3")
	installed, err := artifact.InstallLayout(root, target, expected)
	require.NoError(t, err)
	require.Equal(t, target, installed.BinaryPath)
	require.Equal(t, verified.Descriptor.ArtifactDigest, installed.Descriptor.ArtifactDigest)
	_, err = os.Stat(target + artifact.InstalledManifestSuffix)
	require.NoError(t, err)
	_, err = os.Stat(target + artifact.InstalledDescriptorSuffix)
	require.NoError(t, err)
}

func TestProviderArtifactRejectsTamperAbsentManifestAndIdentityMismatch(t *testing.T) {
	t.Run("tampered binary", func(t *testing.T) {
		root := writeLayout(t, []byte("original"), artifactManifest)
		require.NoError(t, os.WriteFile(filepath.Join(root, "provider-fixture"), []byte("tampered"), 0o755))
		_, err := artifact.VerifyLayout(root, providerAgent("1.2.3"))
		require.ErrorContains(t, err, "binary digest mismatch")
	})
	t.Run("absent manifest", func(t *testing.T) {
		root := writeLayout(t, []byte("original"), artifactManifest)
		require.NoError(t, os.Remove(filepath.Join(root, "provider.codefly.yaml")))
		_, err := artifact.VerifyLayout(root, providerAgent("1.2.3"))
		require.ErrorContains(t, err, "read provider manifest")
	})
	t.Run("requested version mismatch", func(t *testing.T) {
		root := writeLayout(t, []byte("original"), artifactManifest)
		_, err := artifact.VerifyLayout(root, providerAgent("1.2.4"))
		require.ErrorContains(t, err, "does not match requested agent")
	})
	t.Run("manifest mismatch", func(t *testing.T) {
		root := writeLayout(t, []byte("original"), artifactManifest)
		changed := strings.Replace(artifactManifest, "version: 1.2.3", "version: 1.2.4", 1)
		require.NoError(t, os.WriteFile(filepath.Join(root, "provider.codefly.yaml"), []byte(changed), 0o600))
		_, err := artifact.VerifyLayout(root, providerAgent("1.2.3"))
		require.ErrorContains(t, err, "manifest digest mismatch")
	})
}

func TestVerifyExecutableDoesNotSubstituteAnotherLayoutBinary(t *testing.T) {
	root := writeLayout(t, []byte("verified"), artifactManifest)
	verified, err := artifact.VerifyExecutable(filepath.Join(root, "provider-fixture"), providerAgent("1.2.3"))
	require.NoError(t, err)
	require.Equal(t, filepath.Join(root, "provider-fixture"), verified.BinaryPath)

	binaryPath := filepath.Join(root, "bin", "provider-fixture")
	require.NoError(t, os.MkdirAll(filepath.Dir(binaryPath), 0o755))
	require.NoError(t, os.WriteFile(binaryPath, []byte("unverified"), 0o755))

	_, err = artifact.VerifyExecutable(binaryPath, providerAgent("1.2.3"))
	require.ErrorContains(t, err, "descriptor binds")
}

func TestProviderArtifactRejectsDowngradeAndNewerState(t *testing.T) {
	currentRoot := writeLayout(t, []byte("current"), artifactManifest)
	nextRoot := writeLayout(t, []byte("next"), strings.Replace(artifactManifest, "version: 1.2.3", "version: 1.2.2", 1))
	current, err := artifact.VerifyLayout(currentRoot, providerAgent("1.2.3"))
	require.NoError(t, err)
	next, err := artifact.VerifyLayout(nextRoot, providerAgent("1.2.2"))
	require.NoError(t, err)
	require.ErrorContains(t, artifact.AdmitVersionTransition(current.Descriptor, next.Descriptor, 1), "downgrade")
	require.ErrorContains(t, artifact.AdmitVersionTransition(current.Descriptor, current.Descriptor, 2), "newer than supported")
}

func writeLayout(t *testing.T, binary []byte, manifestBytes string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "provider-fixture"), binary, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "provider.codefly.yaml"), []byte(manifestBytes), 0o600))
	descriptor, err := artifact.BuildDescriptor("provider-fixture", binary, []byte(manifestBytes))
	require.NoError(t, err)
	descriptorBytes, err := artifact.MarshalDescriptor(descriptor)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, artifact.DescriptorFileName), descriptorBytes, 0o600))
	return root
}

func providerAgent(version string) *resources.Agent {
	return &resources.Agent{
		Kind: resources.ProviderAgent, Publisher: "codefly.dev", Name: "fixture", Version: version,
	}
}

const artifactManifest = `
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
    - id: account-observe
      action: account.observe
      resource: account
      resource_type: account
      reason: Observe the bound account.
      risk: low
      credential_purpose: management
resource_types:
  - id: account
    actions: [observe]
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
    request_byte_budget: 4096
    response_byte_budget: 4096
    read_only: true
    response_schema: account
    credential_purposes: [management]
origin_rules:
  - id: api
    defaults: [https://api.example.com]
    schemes: [https]
    host_patterns: [api.example.com]
    ports: [443]
    binding_override: deny
    private_network_classes: [public]
credential_purposes:
  - id: management
    minimum_scope: Read the bound account.
    permitted_consumer: management
response_schemas:
  - id: account
    fields:
      - selector: {version: v1, path: "$.id"}
        disposition: FORWARD_SAFE
projections: []
sandbox:
  network: deny
state:
  schema_versions: [1]
  import_identity: false
  replace: false
  delete: false
  stepwise_upgrade: true
diagnostic_namespace: provider.fixture.
`
