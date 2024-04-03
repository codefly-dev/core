package network_test

import (
	"context"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"

	"github.com/codefly-dev/core/network"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

type testDnsManager struct{}

func (t testDnsManager) GetDNS(ctx context.Context, svc *configurations.Service, endpointName string) (*basev0.DNS, error) {
	return nil, nil
}

func TestRuntimeNetworkMappingGenerationNoDNS(t *testing.T) {
	ctx := context.Background()
	service, err := configurations.LoadServiceFromDir(ctx, "testdata/endpoints/basic")
	assert.NoError(t, err)
	endpoints, err := service.LoadEndpoints(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(endpoints))

	// Generate runtime mapping
	dnsManager := &testDnsManager{}

	manager, err := network.NewRuntimeManager(ctx, dnsManager)
	assert.NoError(t, err)
	mappings, err := manager.GenerateNetworkMappings(ctx, service, endpoints, configurations.Local())
	assert.NoError(t, err)
	assert.Equal(t, 2, len(mappings))

}
