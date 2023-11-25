package network

import (
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/plugins/endpoints"
	runtimev1 "github.com/codefly-dev/core/proto/v1/go/services/runtime"
)

// ConvertToEnvironmentVariables converts NetworkMapping to environment variables
func ConvertToEnvironmentVariables(nets []*runtimev1.NetworkMapping) ([]string, error) {
	var envs []string
	for _, net := range nets {
		e, err := endpoints.FromProtoEndpoint(net.Endpoint)
		if err != nil {
			return nil, err
		}
		envs = append(envs, configurations.AsEndpointEnvironmentVariable(net.Application, net.Service, e, net.Addresses))
	}
	return envs, nil
}
