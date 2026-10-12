package manifest_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	modulecontract "github.com/codefly-dev/core/contracts/module"
	"github.com/codefly-dev/core/solution/manifest"
)

// consumingManifest consumes three modules: a platform module with the
// viewer's own credential (no request), a documents module it calls on a
// person's behalf, and a chat module it does not.
const consumingManifest = `
schema_version: codefly.solution-manifest/v1
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
    - id: identity
      protocol: connect
      module: platform
      service: identity
      endpoint: connect
      as: identity
    - id: documents
      protocol: rest
      module: documents
      service: documents
      endpoint: rest
      as: documents
      audience: {from: documents/documents-prefix}
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

// values supplies the composition's configuration the audience slots resolve
// from.
var values = modulecontract.MapValues{Public: map[string]map[string]string{
	"documents": {"DOCUMENTS_PREFIX": "documents"},
	"model":     {"MODEL_AUDIENCE": "modelservice"},
}}

func resolvedConsumption(t *testing.T, document, principal string) *modulecontract.Resolved {
	t.Helper()
	m, err := manifest.Load([]byte(document))
	require.NoError(t, err)
	contract, err := m.ConsumptionContract(principal)
	require.NoError(t, err)
	require.NotNil(t, contract)
	resolved, err := contract.Resolve(values)
	require.NoError(t, err)
	return resolved
}

// THE DELIVERY HALF: a solution's declared consumption is a module contract —
// one invoke binding per scoped entry, its audience resolved from the
// composition, bounded by exactly the scopes it declared.
func TestConsumptionContractIsABindingPerScopedEntry(t *testing.T) {
	resolved := resolvedConsumption(t, consumingManifest, "consumer")

	require.Equal(t, "consumer", resolved.Principal)
	require.Equal(t, []modulecontract.ResolvedBinding{{
		ID:         "documents",
		Revision:   1,
		Operations: []string{modulecontract.OperationInvoke},
		Audience:   "documents",
		Scopes: map[string][]string{modulecontract.OperationInvoke: {
			"documents.collections:list", "documents.collections:read", "documents.passages:read",
		}},
	}}, resolved.Bindings, "only the entry that asks for scopes asks for authority")
	require.NotNil(t, resolved.Namespaces)
	require.NotNil(t, resolved.Queues)
	require.NotNil(t, resolved.ScopeCeilings)
	require.Empty(t, resolved.Namespaces)
}

// The audience is the composition's to say, resolved like a contract's: a
// module composed as `model` that verifies `modelservice`, addressed by the
// runtime as `modelservice`. There is no rule tying `as` to the module.
func TestConsumptionAudienceIsResolvedFromTheComposition(t *testing.T) {
	document := strings.Replace(consumingManifest,
		"    - id: chat\n      protocol: http\n      module: assistant\n      service: chat\n      endpoint: http\n",
		"    - id: model\n      protocol: rest\n      module: model\n      service: model\n      endpoint: rest\n      as: modelservice\n      audience: {from: model/model-audience}\n      scopes:\n        - resource_kind: modelservice.profiles\n          actions: [invoke]\n", 1)
	require.NotEqual(t, consumingManifest, document)

	resolved := resolvedConsumption(t, document, "consumer")

	audiences := map[string]string{}
	for _, binding := range resolved.Bindings {
		audiences[binding.ID] = binding.Audience
	}
	require.Equal(t, map[string]string{"documents": "documents", "model": "modelservice"}, audiences)
}

// An unresolved audience fails resolution by the contract's own sentinel, as a
// module contract's would.
func TestAnUnresolvedConsumptionAudienceFailsLikeAContract(t *testing.T) {
	m, err := manifest.Load([]byte(consumingManifest))
	require.NoError(t, err)
	contract, err := m.ConsumptionContract("consumer")
	require.NoError(t, err)
	_, err = contract.Resolve(modulecontract.MapValues{})
	require.ErrorIs(t, err, modulecontract.ErrUnresolvedSlot)
}

// Two scoped endpoints of one module are two bindings at one audience.
func TestTwoScopedEndpointsOfOneModule(t *testing.T) {
	document := strings.Replace(consumingManifest,
		"    - id: chat\n",
		"    - id: documents-grpc\n      protocol: grpc\n      module: documents\n      service: documents\n      endpoint: grpc\n      as: documents-grpc\n      audience: {from: documents/documents-prefix}\n      scopes:\n        - resource_kind: documents.passages\n          actions: [read]\n    - id: chat\n", 1)

	resolved := resolvedConsumption(t, document, "consumer")

	require.Len(t, resolved.Bindings, 2)
	for _, binding := range resolved.Bindings {
		require.Equal(t, "documents", binding.Audience)
	}
}

// The revision is the solution's to bump.
func TestTheConsumptionRevisionIsBumpable(t *testing.T) {
	document := strings.Replace(consumingManifest, "      audience: {from: documents/documents-prefix}\n", "      audience: {from: documents/documents-prefix}\n      revision: 3\n", 1)
	require.Equal(t, uint64(3), resolvedConsumption(t, document, "consumer").Bindings[0].Revision)
}

// A solution that asks for no scopes asks for no authority.
func TestConsumptionContractIsNilWithoutScopes(t *testing.T) {
	m, err := manifest.Load([]byte(validManifest))
	require.NoError(t, err)
	contract, err := m.ConsumptionContract("fixture")
	require.NoError(t, err)
	require.Nil(t, contract)
}

// The scopes are canonical in the digest.
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
}

// THE RUNTIME PROJECTION carries the declared scopes, so a runtime can hold its
// own mints to the declaration the authority document is rendered from.
func TestTheRuntimeProjectionCarriesDeclaredScopes(t *testing.T) {
	m, err := manifest.Load([]byte(consumingManifest))
	require.NoError(t, err)
	var projected []manifest.ConsumedAPI
	require.NoError(t, json.Unmarshal([]byte(m.ConsumedAPIsEnvValue()), &projected))
	byID := map[string]manifest.ConsumedAPI{}
	for _, consumed := range projected {
		byID[consumed.ID] = consumed
	}
	require.Equal(t, []manifest.ConsumedScope{
		{ResourceKind: "documents.collections", Actions: []string{"list", "read"}},
		{ResourceKind: "documents.passages", Actions: []string{"read"}},
	}, byID["documents"].Scopes)
	require.Nil(t, byID["identity"].Scopes, "an entry that asks for nothing carries nothing")
}

func TestManifestRejectsMalformedConsumption(t *testing.T) {
	scoped := "        - resource_kind: documents.passages\n          actions: [read]\n"
	for _, tc := range []struct {
		name    string
		mutate  func(string) string
		message string
	}{
		{"scopes present and empty", func(s string) string {
			return strings.Replace(s, "      scopes:\n"+scoped+"        - resource_kind: documents.collections\n          actions: [read, list]\n", "      scopes: []\n", 1)
		}, "present and empty"},
		{"no audience", func(s string) string {
			return strings.Replace(s, "      audience: {from: documents/documents-prefix}\n", "", 1)
		}, "names no audience"},
		{"a literal audience", func(s string) string {
			return strings.Replace(s, "audience: {from: documents/documents-prefix}", "audience: documents", 1)
		}, "module contract refuses"},
		{"an audience key that does not say it is one", func(s string) string {
			return strings.Replace(s, "audience: {from: documents/documents-prefix}", "audience: {from: documents/documents-endpoint}", 1)
		}, "module contract refuses"},
		{"audience without scopes", func(s string) string {
			return strings.Replace(s, "      as: identity\n", "      as: identity\n      audience: {from: platform/identity-audience}\n", 1)
		}, "asks for no scopes"},
		{"kind not a resource kind", func(s string) string {
			return strings.Replace(s, "resource_kind: documents.passages", "resource_kind: Documents:Passages", 1)
		}, "module contract refuses"},
		{"kind declared twice", func(s string) string {
			return strings.Replace(s, scoped, scoped+scoped, 1)
		}, "module contract refuses"},
		{"no action", func(s string) string {
			return strings.Replace(s, "          actions: [read]\n", "          actions: []\n", 1)
		}, "names no action"},
		{"a wildcard action", func(s string) string {
			return strings.Replace(s, "          actions: [read]\n", "          actions: [\"*\"]\n", 1)
		}, "module contract refuses"},
		{"action declared twice", func(s string) string {
			return strings.Replace(s, "          actions: [read]\n", "          actions: [read, read]\n", 1)
		}, "module contract refuses"},
		{"scopes on an unbound consumption", func(s string) string {
			return strings.Replace(s, "      module: documents\n      service: documents\n      endpoint: rest\n      as: documents\n", "", 1)
		}, "asks for authority at no module"},
		{"a request in a v0 manifest", func(s string) string {
			return strings.Replace(s, "codefly.solution-manifest/v1", "codefly.solution-manifest/v0", 1)
		}, "declare schema_version: codefly.solution-manifest/v1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mutated := tc.mutate(consumingManifest)
			require.NotEqual(t, consumingManifest, mutated, "the mutation must apply")
			_, err := manifest.Load([]byte(mutated))
			require.ErrorContains(t, err, tc.message)
			_, err = manifest.LoadConsumption([]byte(mutated))
			require.ErrorContains(t, err, tc.message, "the consumption-only read refuses what Load refuses in api.consumes")
		})
	}
}

// ONE GRAMMAR: the kinds the module contract refuses are refused here, and the
// ones it admits are admitted — because it is the module contract that judges.
func TestTheConsumptionGrammarIsTheModuleContracts(t *testing.T) {
	for _, kind := range []string{"0documents", "documents.", "documents..x", "a_b", "documents:x", "documents.passages"} {
		document := strings.Replace(consumingManifest, "resource_kind: documents.passages", "resource_kind: "+kind, 1)
		_, manifestErr := manifest.Load([]byte(document))
		contract := "schema: codefly/module-contract/v1\nprincipal: consumer\nnamespaces: []\nqueues: []\nscope_ceilings: []\nbindings:\n  - id: documents\n    operations: [invoke]\n    audience: {from: documents/documents-prefix}\n    scope_ceiling:\n      invoke:\n        - resource_kind: " + kind + "\n          actions: [read]\ndestinations: []\n"
		_, contractErr := modulecontract.Parse([]byte(contract))
		require.Equal(t, contractErr == nil, manifestErr == nil, "kind %q: manifest %v, contract %v", kind, manifestErr, contractErr)
	}
}

func TestManifestRejectsRequestFieldsOnExposedAPI(t *testing.T) {
	exposed := strings.Replace(consumingManifest,
		"    - id: http\n      protocol: http\n",
		"    - id: http\n      protocol: http\n      scopes:\n        - resource_kind: documents.passages\n          actions: [read]\n", 1)
	_, err := manifest.Load([]byte(exposed))
	require.ErrorContains(t, err, "api.exposes[0].scopes is not allowed")
}

// LoadConsumption reads api.consumes and nothing else: a manifest Load refuses
// on a section that has nothing to do with consumption is read, and asks for
// nothing; a scoped one yields the same request Load does.
func TestLoadConsumptionReadsOnlyTheConsumption(t *testing.T) {
	for _, name := range []string{"agentless.yaml", "undeclared-section.yaml"} {
		t.Run(name, func(t *testing.T) {
			data, err := os.ReadFile(filepath.Join("testdata", "consumption", name))
			require.NoError(t, err)
			_, err = manifest.Load(data)
			require.Error(t, err, "the fixture is one Load refuses")

			m, err := manifest.LoadConsumption(data)
			require.NoError(t, err)
			require.NotEmpty(t, m.API.Consumes)
			contract, err := m.ConsumptionContract("consumer")
			require.NoError(t, err)
			require.Nil(t, contract, "it asks for nothing")
		})
	}
	t.Run("scoped-consumer.yaml", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("testdata", "consumption", "scoped-consumer.yaml"))
		require.NoError(t, err)
		strict, err := manifest.Load(data)
		require.NoError(t, err)
		narrow, err := manifest.LoadConsumption(data)
		require.NoError(t, err)
		fromStrict, err := strict.ConsumptionContract("consumer")
		require.NoError(t, err)
		fromNarrow, err := narrow.ConsumptionContract("consumer")
		require.NoError(t, err)
		require.Equal(t, fromStrict, fromNarrow)
		resolved, err := fromNarrow.Resolve(values)
		require.NoError(t, err)
		require.Equal(t, "documents", resolved.Bindings[0].Audience)
		require.Equal(t, []string{"documents:read"}, resolved.Bindings[0].Scopes[modulecontract.OperationInvoke])
	})
	t.Run("an unknown field inside a consumption still fails", func(t *testing.T) {
		data, err := os.ReadFile(filepath.Join("testdata", "consumption", "agentless.yaml"))
		require.NoError(t, err)
		_, err = manifest.LoadConsumption([]byte(strings.Replace(string(data), "          as: assistant\n", "          as: assistant\n          grants: everything\n", 1)))
		require.ErrorContains(t, err, "grants")
	})
}
