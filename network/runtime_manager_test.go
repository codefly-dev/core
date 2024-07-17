package network_test

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"

	"github.com/codefly-dev/core/network"

	"github.com/stretchr/testify/require"
)

type testDnsManager struct{}

func (t testDnsManager) GetDNS(ctx context.Context, svc *resources.ServiceIdentity, endpointName string) (*basev0.DNS, error) {
	return nil, nil
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
