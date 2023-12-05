package configurations

import (
	"fmt"
	"strings"
)

// Endpoint is the fundamental entity that standardize communication between services.
type Endpoint struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	Scope       string `yaml:"scope,omitempty"`
	API         string `yaml:"api,omitempty"`
	// FailOver indicates that this endpoint should fail over to another endpoint
	FailOver *Endpoint `yaml:"fail-over,omitempty"`
}

func (e *Endpoint) Unique(app string, service string) string {
	unique := fmt.Sprintf("%s/%s", app, service)
	// Convention: if Endpoint == API, we skip the Endpoint
	if e.Name != "" && e.Name != e.API {
		unique = fmt.Sprintf("%s/%s", unique, e.Name)
	}
	if e.API != "" {
		return fmt.Sprintf("%s::%s", unique, e.API)
	}
	return unique
}

/* For runtime */

const EndpointPrefix = "CODEFLY_ENDPOINT__"

func SerializeAddresses(addresses []string) string {
	return strings.Join(addresses, " ")
}

func AsEndpointEnvironmentVariableKey(app string, service string, endpoint *Endpoint) string {
	unique := endpoint.Unique(app, service)
	unique = strings.ToUpper(unique)
	unique = strings.Replace(unique, "/", "__", 1)
	unique = strings.Replace(unique, "/", "___", 1)
	unique = strings.Replace(unique, "::", "____", 1)
	return strings.ToUpper(fmt.Sprintf("%s%s", EndpointPrefix, unique))
}

func AsEndpointEnvironmentVariable(app string, service string, endpoint *Endpoint, addresses []string) string {
	return fmt.Sprintf("%s=%s", AsEndpointEnvironmentVariableKey(app, service, endpoint), SerializeAddresses(addresses))
}

func ParseEndpointEnvironmentVariableKey(key string) (string, error) {
	unique, found := strings.CutPrefix(key, EndpointPrefix)
	if !found {
		return Unknown, fmt.Errorf("requires a prefix")
	}
	unique = strings.ToLower(unique)
	tokens := strings.SplitN(unique, "__", 3)
	if len(tokens) < 2 {
		return Unknown, fmt.Errorf("needs to be at least of the form app__svc")
	}
	app := tokens[0]
	svc := tokens[1]
	unique = fmt.Sprintf("%s/%s", app, svc)
	if len(tokens) == 2 {
		return unique, nil
	}
	remaining := tokens[2]
	if api, apiOnly := strings.CutPrefix(remaining, "__"); apiOnly {
		unique = fmt.Sprintf("%s::%s", unique, api)
		return unique, nil
	}
	// We have an endpoint: always as _endpoint or _endpoint____api
	remaining = remaining[1:]
	tokens = strings.Split(remaining, "____")
	if len(tokens) == 1 {
		return fmt.Sprintf("%s/%s", unique, remaining), nil
	} else if len(tokens) == 2 {
		return fmt.Sprintf("%s/%s::%s", unique, tokens[0], tokens[1]), nil
	}
	return Unknown, fmt.Errorf("needs to be at least of the form app__svc___endpoint")

}

type EndpointInstance struct {
	Unique    string
	Addresses []string
}

func ParseEndpointEnvironmentVariable(env string) (*EndpointInstance, error) {
	tokens := strings.Split(env, "=")
	unique, err := ParseEndpointEnvironmentVariableKey(tokens[0])
	if err != nil {
		return nil, err
	}
	values := strings.Split(tokens[1], " ")
	return &EndpointInstance{Unique: unique, Addresses: values}, nil
}
