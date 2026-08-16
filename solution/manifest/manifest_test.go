package manifest_test

import (
	"strings"
	"testing"

	"github.com/codefly-dev/core/solution/manifest"
	"github.com/stretchr/testify/require"
)

func TestManifestLoadValidateAndCanonicalDigestOrderIndependent(t *testing.T) {
	first, err := manifest.Load([]byte(validManifest))
	require.NoError(t, err)

	reordered := strings.Replace(validManifest,
		"    - id: gateway\n      protocol: grpc\n    - id: dashboard\n      protocol: http",
		"    - id: dashboard\n      protocol: http\n    - id: gateway\n      protocol: grpc", 1)
	require.NotEqual(t, validManifest, reordered)
	second, err := manifest.Load([]byte(reordered))
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
	require.True(t, strings.HasPrefix(firstDigest, "sha256:"))
}

func TestManifestRejectsUnknownFieldAndMultipleDocuments(t *testing.T) {
	_, err := manifest.Load([]byte(validManifest + "\nunknown_field: true\n"))
	require.ErrorContains(t, err, "field unknown_field not found")

	_, err = manifest.Load([]byte(validManifest + "\n---\nschema_version: codefly.solution-manifest/v0\n"))
	require.ErrorContains(t, err, "multiple YAML documents")
}

func TestManifestRejectsBadIdentity(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(string) string
		message string
	}{
		{"schema version", func(s string) string {
			return strings.Replace(s, "schema_version: codefly.solution-manifest/v0", "schema_version: codefly.solution-manifest/v9", 1)
		}, "unsupported solution manifest schema version"},
		{"protocol version", func(s string) string {
			return strings.Replace(s, "protocol_version: codefly.solution/v0", "protocol_version: codefly.solution/v9", 1)
		}, "unsupported solution protocol version"},
		{"agent kind", func(s string) string {
			return strings.Replace(s, "kind: codefly:solution", "kind: codefly:provider", 1)
		}, "agent.kind must be"},
		{"agent version latest", func(s string) string {
			return strings.Replace(s, "version: 1.2.3", "version: latest", 1)
		}, "must be concrete"},
		{"agent version non-semver", func(s string) string {
			return strings.Replace(s, "version: 1.2.3", "version: v1", 1)
		}, "must be semantic"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := manifest.Load([]byte(tc.mutate(validManifest)))
			require.ErrorContains(t, err, tc.message)
		})
	}
}

func TestManifestRejectsMalformedDeclarations(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mutate  func(string) string
		message string
	}{
		{"duplicate exposed api", func(s string) string {
			return strings.Replace(s, "    - id: dashboard\n      protocol: http", "    - id: gateway\n      protocol: http", 1)
		}, "duplicated"},
		{"invalid protocol", func(s string) string {
			return strings.Replace(s, "      protocol: http", "      protocol: smtp", 1)
		}, "protocol"},
		{"invalid permission risk", func(s string) string {
			return strings.Replace(s, "    risk: medium", "    risk: catastrophic", 1)
		}, "risk is invalid"},
		{"permission missing reason", func(s string) string {
			return strings.Replace(s, "    reason: Write manifests into the gitops repository.\n", "", 1)
		}, "valid id, action, and reason"},
		{"no lifecycle operation", func(s string) string {
			return strings.Replace(s,
				"  create: true\n  update: true\n  package: true\n  render: true",
				"  create: false\n  update: false\n  package: false\n  render: false", 1)
		}, "at least one operation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := manifest.Load([]byte(tc.mutate(validManifest)))
			require.ErrorContains(t, err, tc.message)
		})
	}
}

func TestManifestAcceptsDescriptorOnlySolution(t *testing.T) {
	descriptorOnly := strings.Replace(validManifest,
		"services:\n  - id: api\n    name: API\n",
		"", 1)
	loaded, err := manifest.Load([]byte(descriptorOnly))
	require.NoError(t, err)
	require.Empty(t, loaded.Services)
}

const validManifest = `
schema_version: codefly.solution-manifest/v0
protocol_version: codefly.solution/v0
agent:
  kind: codefly:solution
  publisher: codefly.dev
  name: fixture
  version: 1.2.3
services:
  - id: api
    name: API
api:
  exposes:
    - id: gateway
      protocol: grpc
    - id: dashboard
      protocol: http
  consumes:
    - id: billing
      protocol: rest
events:
  emits:
    - id: solution.rendered
  consumes:
    - id: workspace.provisioned
ui:
  - id: overview
    slot: solution.summary
needs:
  - id: gitops-repo
    kind: repository
permissions:
  - id: gitops-write
    action: gitops.write
    resource: "gitops:fixture"
    reason: Write manifests into the gitops repository.
    risk: medium
lifecycle:
  create: true
  update: true
  package: true
  render: true
`
