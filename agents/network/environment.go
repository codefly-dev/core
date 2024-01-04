package network

import (
	"github.com/codefly-dev/core/configurations"
	runtimev1 "github.com/codefly-dev/core/generated/go/services/runtime/v1"
)

// ConvertToEnvironmentVariables converts NetworkMapping to environment variables
func ConvertToEnvironmentVariables(nets []*runtimev1.NetworkMapping) ([]string, error) {
	var envs []string
	for _, net := range nets {
		e := configurations.FromProtoEndpoint(net.Endpoint)
		envs = append(envs, configurations.AsEndpointEnvironmentVariable(e, net.Addresses))
		// Add environment variables for Rest path
		envs = append(envs, configurations.AsRestRouteEnvironmentVariable(net.Endpoint)...)
	}
	return envs, nil
}
