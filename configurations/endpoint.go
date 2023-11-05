package configurations

import (
	"fmt"
	"strings"
)

type (
	Protocol     string
	ApiFramework string
)

const (
	RestApiFramework    ApiFramework = "rest"
	GraphQLApiFramework ApiFramework = "graphql"
)

type Api struct {
	Protocol  Protocol     `yaml:"protocol"`
	Framework ApiFramework `yaml:"framework,omitempty"`
}

type Endpoint struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Public      bool   `yaml:"public,omitempty"`
	Api         *Api   `yaml:"api,omitempty"`
	// FailOver indicates that this endpoint should fail over to another endpoint
	FailOver *Endpoint `yaml:"fail-over,omitempty"`
}

func (e *Endpoint) Reference() *EndpointReference {
	return &EndpointReference{}
}

/* For runtime */

const EndpointPrefix = "CODEFLY-ENDPOINT_"

func SerializeAddresses(addresses []string) string {
	return strings.Join(addresses, " ")
}

func AsEndpointEnvironmentVariable(reference string, addresses []string) string {
	return fmt.Sprintf("%s%s=%s", EndpointPrefix, strings.ToUpper(reference), SerializeAddresses(addresses))
}

func ParseEndpointEnvironmentVariable(env string) (string, []string) {
	tokens := strings.Split(env, "=")
	reference := strings.ToLower(tokens[0])
	// Namespace break
	reference = strings.Replace(reference, "_", ".", 1)
	reference = strings.Replace(reference, "_", "::", 1)
	values := strings.Split(tokens[1], " ")
	return reference, values
}
