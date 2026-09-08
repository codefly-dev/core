package manifest_test

import (
	"strings"
	"testing"

	"github.com/codefly-dev/core/solution/manifest"
	"github.com/stretchr/testify/require"
)

func TestConsumedAPIsRoundTrip(t *testing.T) {
	m, err := manifest.Load([]byte(validManifest))
	require.NoError(t, err)

	consumed := m.ConsumedAPIs()
	require.Equal(t, []manifest.ConsumedAPI{{
		ID:       "accounts",
		Module:   "saas-starter",
		Service:  "accounts",
		Endpoint: "connect",
		Protocol: "connect",
		As:       "accounts",
	}}, consumed)

	value := m.ConsumedAPIsEnvValue()
	require.NotEmpty(t, value)

	parsed, err := manifest.ParseConsumedAPIs(value)
	require.NoError(t, err)
	require.Equal(t, consumed, parsed)
}

func TestConsumedAPIsCanonicalOrder(t *testing.T) {
	reordered := strings.Replace(validManifest,
		"  consumes:\n    - id: accounts\n      protocol: connect\n      module: saas-starter\n      service: accounts\n      endpoint: connect\n      version: \">=0.1.0 <0.2.0\"\n      services: [AuditService, DatasourceService]\n      as: accounts",
		"  consumes:\n    - id: billing\n      protocol: grpc\n      module: saas-starter\n      service: billing\n      endpoint: grpc\n      as: billing\n    - id: accounts\n      protocol: connect\n      module: saas-starter\n      service: accounts\n      endpoint: connect\n      as: accounts", 1)
	require.NotEqual(t, validManifest, reordered)

	m, err := manifest.Load([]byte(reordered))
	require.NoError(t, err)

	value := m.ConsumedAPIsEnvValue()
	require.Equal(t,
		`[{"id":"accounts","module":"saas-starter","service":"accounts","endpoint":"connect","protocol":"connect","as":"accounts"},`+
			`{"id":"billing","module":"saas-starter","service":"billing","endpoint":"grpc","protocol":"grpc","as":"billing"}]`,
		value)
}

func TestConsumedAPIsEmptyWhenNoneDeclared(t *testing.T) {
	withoutConsumes := strings.Replace(validManifest,
		"  consumes:\n    - id: accounts\n      protocol: connect\n      module: saas-starter\n      service: accounts\n      endpoint: connect\n      version: \">=0.1.0 <0.2.0\"\n      services: [AuditService, DatasourceService]\n      as: accounts\n",
		"", 1)
	require.NotContains(t, withoutConsumes, "saas-starter")

	m, err := manifest.Load([]byte(withoutConsumes))
	require.NoError(t, err)

	require.Nil(t, m.ConsumedAPIs())
	require.Empty(t, m.ConsumedAPIsEnvValue())
}

func TestConsumedAPIsSkipsUnboundEntries(t *testing.T) {
	// An all-empty binding (only id + protocol) is a valid consumed API per
	// validateConsumedAPIs, but names no producing endpoint.
	withUnbound := strings.Replace(validManifest,
		"      as: accounts\n",
		"      as: accounts\n    - id: metrics\n      protocol: grpc\n", 1)
	require.NotEqual(t, validManifest, withUnbound)

	m, err := manifest.Load([]byte(withUnbound))
	require.NoError(t, err)

	consumed := m.ConsumedAPIs()
	require.Len(t, consumed, 1)
	require.Equal(t, "accounts", consumed[0].ID)
	require.NotContains(t, m.ConsumedAPIsEnvValue(), "metrics")
}

func TestConsumedAPIsAllUnboundYieldsNil(t *testing.T) {
	allUnbound := strings.Replace(validManifest,
		"  consumes:\n    - id: accounts\n      protocol: connect\n      module: saas-starter\n      service: accounts\n      endpoint: connect\n      version: \">=0.1.0 <0.2.0\"\n      services: [AuditService, DatasourceService]\n      as: accounts\n",
		"  consumes:\n    - id: accounts\n      protocol: connect\n", 1)
	require.NotContains(t, allUnbound, "saas-starter")

	m, err := manifest.Load([]byte(allUnbound))
	require.NoError(t, err)

	require.Nil(t, m.ConsumedAPIs())
	require.Empty(t, m.ConsumedAPIsEnvValue())
}

func TestParseConsumedAPIs(t *testing.T) {
	nilTargets, err := manifest.ParseConsumedAPIs("")
	require.NoError(t, err)
	require.Nil(t, nilTargets)

	_, err = manifest.ParseConsumedAPIs("{not json")
	require.ErrorContains(t, err, manifest.APIConsumesEnvironmentVariable)
}
