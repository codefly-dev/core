package network

import (
	"context"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
)

type Manager interface {
	GenerateNetworkMappings(ctx context.Context, env *resources.Environment, workspace *resources.Workspace, service *resources.ServiceIdentity, endpoints []*basev0.Endpoint) ([]*basev0.NetworkMapping, error)
	GetNamespace(ctx context.Context, env *resources.Environment, workspace *resources.Workspace, service *resources.ServiceIdentity) (string, error)
}
