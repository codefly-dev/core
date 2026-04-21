package network_test

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"

	"github.com/codefly-dev/core/network"

	"github.com/stretchr/testify/require"
)

// testDnsManager returns no DNS for any endpoint. Used to drive the
// named-port branch of GenerateNetworkMappings.
type testDnsManager struct{}

func (t testDnsManager) GetDNS(ctx context.Context, svc *resources.ServiceIdentity, endpointName string) (*basev0.DNS, error) {
	return nil, nil
}

// fixedDNSManager returns a fixed DNS entry for every endpoint. Used to
// drive the external-DNS branch of GenerateNetworkMappings — the branch
// that was silently producing only Public access instances and breaking
// every agent that looks up by Native or Container.
type fixedDNSManager struct {
	host string
	port uint32
}

func (m *fixedDNSManager) GetDNS(ctx context.Context, svc *resources.ServiceIdentity, endpointName string) (*basev0.DNS, error) {
	return &basev0.DNS{
		Name:     svc.Unique(),
		Module:   svc.Module,
		Service:  svc.Name,
		Endpoint: endpointName,
		Host:     m.host,
		Port:     m.port,
		Secured:  false,
	}, nil
}

func TestRuntimeNetworkMappingGenerationNoDNS(t *testing.T) {
	ctx := context.Background()
	workspace := &resources.Workspace{
		Name: "test-workspace",
	}
	service, err := resources.LoadServiceFromDir(ctx, "testdata/endpoints/basic")
	require.NoError(t, err)
	service.WithModule("test-module")

	endpoints, err := service.LoadEndpoints(ctx)
	require.NoError(t, err)
	require.Equal(t, 2, len(endpoints))

	// Generate runtime mapping
	dnsManager := &testDnsManager{}

	identity, err := service.Identity()
	require.NoError(t, err)

	manager, err := network.NewRuntimeManager(ctx, dnsManager)
	require.NoError(t, err)
	mappings, err := manager.GenerateNetworkMappings(ctx, resources.LocalEnvironment(), workspace, identity, endpoints)
	require.NoError(t, err)
	require.Equal(t, 2, len(mappings))
}

// TestRuntimeNetworkMappingAccessKinds_NoDNS asserts that the named-port
// branch produces one Container instance, one Native instance, plus one
// Public instance iff the endpoint is public-visibility.
//
// This is the path non-external endpoints (e.g. grpc, rest on regular
// services) take. It's the happy path — every agent lookup (Container,
// Native, Public) must resolve to exactly one instance.
func TestRuntimeNetworkMappingAccessKinds_NoDNS(t *testing.T) {
	ctx := context.Background()
	workspace := &resources.Workspace{Name: "test-workspace"}
	service, err := resources.LoadServiceFromDir(ctx, "testdata/endpoints/basic")
	require.NoError(t, err)
	service.WithModule("test-module")

	endpoints, err := service.LoadEndpoints(ctx)
	require.NoError(t, err)

	identity, err := service.Identity()
	require.NoError(t, err)

	manager, err := network.NewRuntimeManager(ctx, &testDnsManager{})
	require.NoError(t, err)
	mappings, err := manager.GenerateNetworkMappings(ctx, resources.LocalEnvironment(), workspace, identity, endpoints)
	require.NoError(t, err)
	require.Equal(t, 2, len(mappings))

	// Basic testdata service.codefly.yaml declares:
	//   grpc   (default visibility)
	//   rest   visibility: public
	for _, mapping := range mappings {
		kinds := accessKindsOf(mapping)
		require.Contains(t, kinds, resources.NetworkAccessContainer,
			"mapping %s missing container access", mapping.Endpoint.Name)
		require.Contains(t, kinds, resources.NetworkAccessNative,
			"mapping %s missing native access", mapping.Endpoint.Name)
		if mapping.Endpoint.Visibility == resources.VisibilityPublic {
			require.Contains(t, kinds, resources.NetworkAccessPublic,
				"public endpoint %s missing public access", mapping.Endpoint.Name)
		}
	}
}

// TestRuntimeNetworkMappingAccessKinds_ExternalDNS regression-tests the
// bug that broke postgres/neo4j agent init on local `codefly run`:
//
// When an endpoint is visibility=external AND DNS is configured, the
// generator returned two instances BOTH with Public access — the
// ContainerInstance/NativeInstance wrappers were identity functions and
// did not overwrite the access kind DNS() sets. As a result, agents
// running natively on the host (which look up by Access=Native) failed
// with "no network instance for endpoint".
//
// After the fix, the external-DNS branch must emit exactly one Container
// instance and one Native instance, both pointing at the DNS host.
func TestRuntimeNetworkMappingAccessKinds_ExternalDNS(t *testing.T) {
	ctx := context.Background()
	workspace := &resources.Workspace{Name: "test-workspace"}

	identity := &resources.ServiceIdentity{
		Name:    "postgres",
		Module:  "infra",
		Version: "0.0.0",
	}

	// Minimal external endpoint — mirrors what modules/infra/services/postgres
	// declares in its service.codefly.yaml.
	endpoint := &basev0.Endpoint{
		Name:       "tcp",
		Module:     identity.Module,
		Service:    identity.Name,
		Api:        "tcp",
		Visibility: resources.VisibilityExternal,
	}

	dnsManager := &fixedDNSManager{host: "localhost", port: 5432}

	manager, err := network.NewRuntimeManager(ctx, dnsManager)
	require.NoError(t, err)
	mappings, err := manager.GenerateNetworkMappings(ctx, resources.LocalEnvironment(), workspace, identity, []*basev0.Endpoint{endpoint})
	require.NoError(t, err)
	require.Len(t, mappings, 1)

	inst := mappings[0].Instances
	require.Len(t, inst, 2, "external-DNS path should emit container + native instances")

	kinds := accessKindsOf(mappings[0])
	require.Contains(t, kinds, resources.NetworkAccessContainer,
		"external-DNS endpoint missing container access: got %v", kinds)
	require.Contains(t, kinds, resources.NetworkAccessNative,
		"external-DNS endpoint missing native access: got %v", kinds)

	// And every emitted instance points at the DNS host/port — i.e. the
	// wrappers only rewrote Access, not Hostname/Port.
	for _, i := range inst {
		require.Equal(t, "localhost", i.Hostname)
		require.Equal(t, uint32(5432), i.Port)
	}
}

// TestRuntimeNetworkMappingAccessKinds_FindNative proves the end-to-end
// property agents depend on: given a generated mapping, the core helper
// FindNetworkInstanceInNetworkMappings with a Native lookup key must
// succeed for any endpoint reachable natively on the host. This is the
// exact call postgres/neo4j agents make during runtime::init.
func TestRuntimeNetworkMappingAccessKinds_FindNative(t *testing.T) {
	ctx := context.Background()
	workspace := &resources.Workspace{Name: "test-workspace"}

	identity := &resources.ServiceIdentity{
		Name:    "postgres",
		Module:  "infra",
		Version: "0.0.0",
	}
	endpoint := &basev0.Endpoint{
		Name:       "tcp",
		Module:     identity.Module,
		Service:    identity.Name,
		Api:        "tcp",
		Visibility: resources.VisibilityExternal,
	}

	dnsManager := &fixedDNSManager{host: "localhost", port: 5432}
	manager, err := network.NewRuntimeManager(ctx, dnsManager)
	require.NoError(t, err)
	mappings, err := manager.GenerateNetworkMappings(ctx, resources.LocalEnvironment(), workspace, identity, []*basev0.Endpoint{endpoint})
	require.NoError(t, err)

	found, err := resources.FindNetworkInstanceInNetworkMappings(ctx, mappings, endpoint, resources.NewNativeNetworkAccess())
	require.NoError(t, err, "agent-style Native lookup must succeed after fix")
	require.NotNil(t, found)
	require.Equal(t, resources.NetworkAccessNative, found.Access.Kind)
	require.Equal(t, "localhost", found.Hostname)
	require.Equal(t, uint32(5432), found.Port)

	foundC, err := resources.FindNetworkInstanceInNetworkMappings(ctx, mappings, endpoint, resources.NewContainerNetworkAccess())
	require.NoError(t, err, "Container lookup must also succeed")
	require.NotNil(t, foundC)
	require.Equal(t, resources.NetworkAccessContainer, foundC.Access.Kind)
}

// accessKindsOf returns the set of Access.Kind strings present in a
// mapping's instances. Handy for declarative assertions on the
// container/native/public tri-split.
func accessKindsOf(m *basev0.NetworkMapping) []string {
	out := make([]string, 0, len(m.Instances))
	for _, i := range m.Instances {
		if i.Access != nil {
			out = append(out, i.Access.Kind)
		}
	}
	return out
}
