package manifest_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	modulecontract "github.com/codefly-dev/core/contracts/module"
	"github.com/codefly-dev/core/solution/manifest"
)

// consumingManifest is a solution that consumes three modules: the host's
// accounts (with the viewer's own credential — no scopes), a documents module
// it calls on a person's behalf, and a chat module it does not.
const consumingManifest = `
schema_version: codefly.solution-manifest/v0
protocol_version: codefly.solution/v0
agent:
  kind: codefly:solution
  publisher: codefly.dev
  name: fixture
  version: 1.2.3
api:
  exposes:
    - id: http
      protocol: http
  consumes:
    - id: accounts
      protocol: connect
      module: saas
      service: accounts
      endpoint: connect
      as: accounts
    - id: documents
      protocol: rest
      module: documents
      service: documents
      endpoint: rest
      as: documents
      scopes:
        - resource_kind: documents.passages
          actions: [read]
        - resource_kind: documents.collections
          actions: [read, list]
    - id: chat
      protocol: http
      module: assistant
      service: chat
      endpoint: http
lifecycle:
  render: true
`

// THE DELIVERY HALF: a solution's declared consumption yields the binding its
// authority document is rendered from — addressed to the consumed module, invoke
// only, bounded by exactly the scopes it declared.
func TestConsumptionAuthorityYieldsABindingPerScopedConsumption(t *testing.T) {
	m, err := manifest.Load([]byte(consumingManifest))
	require.NoError(t, err)

	resolved := m.ConsumptionAuthority("knowledge")

	require.NotNil(t, resolved)
	require.Equal(t, "knowledge", resolved.Principal)
	require.Equal(t, []modulecontract.ResolvedBinding{{
		ID:         "documents",
		Revision:   1,
		Operations: []string{modulecontract.OperationInvoke},
		Audience:   "documents",
		Scopes: map[string][]string{modulecontract.OperationInvoke: {
			"documents.collections:list", "documents.collections:read", "documents.passages:read",
		}},
	}}, resolved.Bindings, "only the consumption that asks for scopes asks for authority")
	// "Declares none" is written out, never dropped.
	require.NotNil(t, resolved.Namespaces)
	require.NotNil(t, resolved.Queues)
	require.NotNil(t, resolved.ScopeCeilings)
	require.Empty(t, resolved.Namespaces)
}

// A solution that asks for no scopes asks for no authority.
func TestConsumptionAuthorityIsNilWithoutScopes(t *testing.T) {
	m, err := manifest.Load([]byte(validManifest))
	require.NoError(t, err)
	require.Nil(t, m.ConsumptionAuthority("fixture"))
}

// The scopes are canonical in the digest: the same declaration written in
// another order is the same manifest.
func TestConsumedScopesOrderIndependentDigest(t *testing.T) {
	first, err := manifest.Load([]byte(consumingManifest))
	require.NoError(t, err)
	reordered := strings.Replace(consumingManifest,
		"        - resource_kind: documents.passages\n          actions: [read]\n        - resource_kind: documents.collections\n          actions: [read, list]\n",
		"        - resource_kind: documents.collections\n          actions: [list, read]\n        - resource_kind: documents.passages\n          actions: [read]\n", 1)
	require.NotEqual(t, consumingManifest, reordered)
	second, err := manifest.Load([]byte(reordered))
	require.NoError(t, err)

	firstDigest, err := first.Digest()
	require.NoError(t, err)
	secondDigest, err := second.Digest()
	require.NoError(t, err)
	require.Equal(t, firstDigest, secondDigest)
	require.Equal(t, first.ConsumptionAuthority("p"), second.ConsumptionAuthority("p"))
}

func TestManifestRejectsMalformedConsumedScopes(t *testing.T) {
	scoped := "        - resource_kind: documents.passages\n          actions: [read]\n"
	for _, tc := range []struct {
		name    string
		mutate  func(string) string
		message string
	}{
		{"kind not a resource kind", func(s string) string {
			return strings.Replace(s, "resource_kind: documents.passages", "resource_kind: Documents:Passages", 1)
		}, `resource_kind "Documents:Passages" is not a resource kind`},
		{"kind declared twice", func(s string) string {
			return strings.Replace(s, scoped, scoped+scoped, 1)
		}, `"documents.passages" is declared twice`},
		{"no action", func(s string) string {
			return strings.Replace(s, "          actions: [read]\n", "          actions: []\n", 1)
		}, "names no action"},
		{"action not an action", func(s string) string {
			return strings.Replace(s, "          actions: [read]\n", "          actions: [\"*\"]\n", 1)
		}, `actions entry "*" is not an action`},
		{"action declared twice", func(s string) string {
			return strings.Replace(s, "          actions: [read]\n", "          actions: [read, read]\n", 1)
		}, `actions entry "read" is declared twice`},
		{"as names another audience", func(s string) string {
			return strings.Replace(s, "      as: documents\n", "      as: docs\n", 1)
		}, `asks for scopes at module "documents" but names it "docs" with as`},
		{"scopes on an unbound consumption", func(s string) string {
			return strings.Replace(s,
				"      module: documents\n      service: documents\n      endpoint: rest\n      as: documents\n", "", 1)
		}, "asks for authority at no module"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := tc.mutate(consumingManifest)
			require.NotEqual(t, consumingManifest, mutated, "the mutation must apply")
			_, err := manifest.Load([]byte(mutated))
			require.ErrorContains(t, err, tc.message)
		})
	}
}

func TestManifestRejectsScopesOnExposedAPI(t *testing.T) {
	exposed := strings.Replace(consumingManifest,
		"    - id: http\n      protocol: http\n",
		"    - id: http\n      protocol: http\n      scopes:\n        - resource_kind: documents.passages\n          actions: [read]\n", 1)
	_, err := manifest.Load([]byte(exposed))
	require.ErrorContains(t, err, "api.exposes[0].scopes is not allowed")
}
