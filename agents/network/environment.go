package network

import (
	"context"

	"github.com/codefly-dev/core/configurations"
	runtimev0 "github.com/codefly-dev/core/generated/go/services/runtime/v0"
)

// ExtractEndpointEnvironmentVariables converts NetworkMapping endpoint data to environment variables
func ExtractEndpointEnvironmentVariables(ctx context.Context, nets []*runtimev0.NetworkMapping) ([]string, error) {
	var envs []string
	for _, net := range nets {
		e := configurations.FromProtoEndpoint(net.Endpoint)
		endpoint := configurations.AsEndpointEnvironmentVariable(ctx, e, net.Addresses)
		envs = append(envs, endpoint)
	}
	return envs, nil
}

// ExtractRestRoutesEnvironmentVariables converts NetworkMapping endpoint REST data to environment variables
func ExtractRestRoutesEnvironmentVariables(ctx context.Context, nets []*runtimev0.NetworkMapping) ([]string, error) {
	var envs []string
	for _, net := range nets {
		envs = append(envs, configurations.AsRestRouteEnvironmentVariable(ctx, net.Endpoint)...)
	}
	return envs, nil
}
