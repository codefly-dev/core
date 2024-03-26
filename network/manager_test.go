package network_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/network"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestRuntimeNetworkMappingGenerationNoDNS(t *testing.T) {
	ctx := context.Background()
	service, err := configurations.LoadServiceFromDir(ctx, "testdata/endpoints/basic")
	assert.NoError(t, err)
	endpoints, err := service.LoadEndpoints(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(endpoints))

	// Generate runtime mapping
	manager, err := network.NewManager(ctx)
	assert.NoError(t, err)
	mappings, err := manager.GenerateNetworkMappings(ctx, service, endpoints)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(mappings))

}
