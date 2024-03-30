package network

import (
	"context"

	"github.com/codefly-dev/core/configurations"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type Manager interface {
	GenerateNetworkMappings(ctx context.Context, service *configurations.Service, endpoints []*basev0.Endpoint) ([]*basev0.NetworkMapping, error)
	GetNamespace(ctx context.Context, service *configurations.Service, env *configurations.Environment) (string, error)
}
