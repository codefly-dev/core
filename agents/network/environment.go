package network

import (
	"context"

	"github.com/codefly-dev/core/configurations"
	runtimev0 "github.com/codefly-dev/core/generated/go/services/runtime/v0"
	"github.com/codefly-dev/core/wool"
)

// ConvertToEnvironmentVariables converts NetworkMapping to environment variables
func ConvertToEnvironmentVariables(ctx context.Context, nets []*runtimev0.NetworkMapping) ([]string, error) {
	w := wool.Get(ctx).In("ConvertToEnvironmentVariables")
	var envs []string
	for _, net := range nets {
		e := configurations.FromProtoEndpoint(net.Endpoint)
		endpoint := configurations.AsEndpointEnvironmentVariable(ctx, e, net.Addresses)
		w.Debug("created", wool.Field("endpoint", endpoint))
		envs = append(envs, endpoint)
		// Add environment variables for Rest path
		envs = append(envs, configurations.AsRestRouteEnvironmentVariable(ctx, net.Endpoint)...)
	}
	return envs, nil
}
