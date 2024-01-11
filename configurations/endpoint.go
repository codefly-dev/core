package configurations

import (
	"context"
	"fmt"
	"strings"

	"github.com/codefly-dev/core/configurations/standards"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/wool"
)

type Visibility = string

const (
	VisibilityApplication Visibility = "application"
	VisibilityPublic      Visibility = "public"
)

// Endpoint is the fundamental entity that standardize communication between services.
type Endpoint struct {
	Name        string `yaml:"name"`
	Service     string `yaml:"service,omitempty"`
	Application string `yaml:"application,omitempty"`
	Description string `yaml:"description,omitempty"`
	Visibility  string `yaml:"visibility,omitempty"`
	API         string `yaml:"api,omitempty"`
	// FailOver indicates that this endpoint should fail over to another endpoint
	FailOver *Endpoint `yaml:"fail-over,omitempty"`
}

func (e *Endpoint) Unique() string {
	if e.Name == "" {
		return fmt.Sprintf("%s/%s", e.Application, e.Service)
	}
	unique := fmt.Sprintf("%s/%s/%s", e.Application, e.Service, e.Name)
	// Convention: if Endpoint Name == API, we skip the Name
	if e.API != Unknown && e.Name != e.API {
		return fmt.Sprintf("%s::%s", unique, e.API)
	}
	return unique
}

func ParseEndpoint(unique string) (*Endpoint, error) {
	// Do we have the explicit APIva
	endpoint := &Endpoint{}
	if strings.Contains(unique, "::") {
		tokens := strings.Split(unique, "::")
		if len(tokens) != 2 {
			return nil, fmt.Errorf("endpoint needs to be of the form app/svc/endpoint::api")
		}
		endpoint.API = tokens[1]
		endpoint.Name = endpoint.API
		unique = tokens[0]
	}

	tokens := strings.Split(unique, "/")
	if len(tokens) == 3 {
		unique = strings.Join(tokens[:2], "/")
		endpoint.Name = tokens[2]
	}
	in, err := ParseServiceUnique(unique)
	if err != nil {
		return nil, err
	}
	endpoint.Service = in.Name
	endpoint.Application = in.Application

	if endpoint.API == "" {
		endpoint.API = Unknown
	}
	return endpoint, nil
}

func (e *Endpoint) AsReference() *EndpointReference {
	return &EndpointReference{
		Name: e.Name,
	}
}

/* For runtime */

const EndpointPrefix = "CODEFLY_ENDPOINT__"

func SerializeAddresses(addresses []string) string {
	return strings.Join(addresses, " ")
}

func EndpointEnvironmentVariableKey(endpoint *Endpoint) string {
	unique := endpoint.Unique()
	unique = strings.ToUpper(unique)
	unique = strings.Replace(unique, "/", "__", 1)
	unique = strings.Replace(unique, "/", "___", 1)
	unique = strings.Replace(unique, "::", "____", 1)
	return strings.ToUpper(fmt.Sprintf("%s%s", EndpointPrefix, unique))
}

func AsEndpointEnvironmentVariable(endpoint *Endpoint, addresses []string) string {
	return fmt.Sprintf("%s=%s", EndpointEnvironmentVariableKey(endpoint), SerializeAddresses(addresses))
}

func AsRestRouteEnvironmentVariable(endpoint *basev0.Endpoint) []string {
	var envs []string
	if rest := HasRest(context.Background(), endpoint.Api); rest != nil {
		for _, route := range rest.Routes {
			envs = append(envs, RestRoutesAsEnvironmentVariable(endpoint, route))
		}
	}
	return envs

}

const Unknown = "unknown"
const NA = "NA"

type EndpointInstance struct {
	*Endpoint
	Addresses []string
}

func (instance *EndpointInstance) Address() (string, error) {
	if len(instance.Addresses) != 1 {
		return "", fmt.Errorf("endpoint instance has more than one address")
	}
	return instance.Addresses[0], nil
}

func (instance *EndpointInstance) PortAddress() (string, error) {
	address, err := instance.Address()
	if err != nil {
		return "", err
	}
	tokens := strings.Split(address, ":")
	if len(tokens) == 2 {
		return fmt.Sprintf(":%s", tokens[1]), nil
	}
	return "", fmt.Errorf("endpoint instance address does not have a port")
}

type NilAPIError struct {
	name string
}

func (err *NilAPIError) Error() string {
	return fmt.Sprintf("endpoint <%s> api is nil", err.name)
}

type UnknownAPIError struct {
	api *basev0.API
}

func (err *UnknownAPIError) Error() string {
	return fmt.Sprintf("unknow api: <%v>", err.api)
}

func WhichAPIFromEndpoint(endpoint *basev0.Endpoint) (string, error) {
	if endpoint.Api == nil {
		return "", &NilAPIError{name: endpoint.Name}
	}
	return WhichAPI(endpoint.Api)
}

func WhichAPI(api *basev0.API) (string, error) {
	switch api.Value.(type) {
	case *basev0.API_Grpc:
		return standards.GRPC, nil
	case *basev0.API_Rest:
		return standards.REST, nil
	case *basev0.API_Http:
		return standards.HTTP, nil
	case *basev0.API_Tcp:
		return standards.TCP, nil
	default:
		return "", &UnknownAPIError{api}
	}
}

func StandardPort(api *basev0.API) (int, error) {
	switch api.Value.(type) {
	case *basev0.API_Grpc:
		return 9090, nil
	case *basev0.API_Rest:
		return 8080, nil
	case *basev0.API_Http:
		return 8080, nil
	case *basev0.API_Tcp:
		return 7070, nil
	default:
		return 0, &UnknownAPIError{api}
	}
}

type NilEndpointError struct{}

func (n NilEndpointError) Error() string {
	return "endpoint is nil"
}

func EndpointBaseProto(e *Endpoint) *basev0.Endpoint {
	return &basev0.Endpoint{
		Name:        e.Name,
		Application: e.Application,
		Service:     e.Service,
		Visibility:  e.Visibility,
		Description: e.Description,
	}
}

func FromProtoEndpoint(e *basev0.Endpoint) *Endpoint {
	return &Endpoint{
		Name:        e.Name,
		Application: e.Application,
		Service:     e.Service,
		Visibility:  e.Visibility,
		Description: e.Description,
		API:         FromProtoAPI(e.Api),
	}
}

func FromProtoEndpoints(es ...*basev0.Endpoint) ([]*Endpoint, error) {
	var endpoints []*Endpoint
	for _, e := range es {
		endpoints = append(endpoints, FromProtoEndpoint(e))
	}
	return endpoints, nil
}

func EndpointDestination(e *basev0.Endpoint) string {
	return fmt.Sprintf("%s/%s/%s[%s]", e.Application, e.Service, e.Name, FromProtoAPI(e.Api))
}

func FromProtoAPI(api *basev0.API) string {
	if api == nil {
		return Unknown
	}
	switch api.Value.(type) {
	case *basev0.API_Grpc:
		return standards.GRPC
	case *basev0.API_Rest:
		return standards.REST
	case *basev0.API_Http:
		return standards.HTTP
	case *basev0.API_Tcp:
		return standards.TCP
	default:
		return Unknown
	}
}

func LightAPI(api *basev0.API) *basev0.API {
	switch api.Value.(type) {
	case *basev0.API_Grpc:
		return &basev0.API{
			Value: &basev0.API_Grpc{},
		}
	case *basev0.API_Rest:
		return &basev0.API{
			Value: &basev0.API_Rest{
				Rest: &basev0.RestAPI{Routes: api.Value.(*basev0.API_Rest).Rest.Routes},
			},
		}
	case *basev0.API_Http:
		return &basev0.API{
			Value: &basev0.API_Http{},
		}
	case *basev0.API_Tcp:
		return &basev0.API{
			Value: &basev0.API_Tcp{},
		}
	default:
		return nil
	}
}

func Light(e *basev0.Endpoint) *basev0.Endpoint {
	return &basev0.Endpoint{
		Name:        e.Name,
		Visibility:  e.Visibility,
		Description: e.Description,
		Api:         e.Api,
	}
}

func FlattenRestRoutes(_ context.Context, endpoints []*basev0.Endpoint) []*basev0.RestRoute {
	var routes []*basev0.RestRoute
	for _, ep := range endpoints {
		if rest := ep.Api.GetRest(); rest != nil {
			routes = append(routes, rest.Routes...)
		}
	}
	return routes
}

func DetectNewRoutesFromEndpoints(ctx context.Context, known []*RestRoute, endpoints []*basev0.Endpoint) []*RestRoute {
	w := wool.Get(ctx).In("DetectNewRoutes")
	// TODO: Don't remember what I wanted to do
	for _, e := range endpoints {
		w.Info("TODO: forgot what it was", wool.Field("endpoint", e))
	}

	var newRoutes []*RestRoute
	for _, endpoint := range endpoints {
		if rest := HasRest(ctx, endpoint.Api); rest != nil {
			for _, route := range rest.Routes {
				potential := &RestRoute{
					Application: endpoint.Application,
					Service:     endpoint.Service,
					Path:        route.Path,
					Methods:     ConvertMethods(route.Methods),
				}
				if !ContainsRoute(known, potential) {
					newRoutes = append(newRoutes, potential)
				}
			}
		}
	}

	return newRoutes
}

func FindEndpointForRoute(ctx context.Context, endpoints []*basev0.Endpoint, route *RestRoute) *basev0.Endpoint {
	for _, e := range endpoints {
		if e.Application == route.Application && e.Service == route.Service && HasRest(ctx, e.Api) != nil {
			return e
		}
	}
	return nil
}

func HasRest(_ context.Context, api *basev0.API) *basev0.RestAPI {
	if api == nil {
		return nil
	}
	switch v := api.Value.(type) {
	case *basev0.API_Rest:
		return v.Rest
	default:
		return nil
	}
}

func Condensed(es []*basev0.Endpoint) []string {
	var outs []string
	for _, e := range es {
		outs = append(outs, EndpointDestination(e))
	}
	return outs
}

type EndpointSummary struct {
	Count   int
	Uniques []string
}

func MakeEndpointSummary(endpoints []*basev0.Endpoint) EndpointSummary {
	sum := EndpointSummary{}
	sum.Count = len(endpoints)
	for _, e := range endpoints {
		sum.Uniques = append(sum.Uniques, EndpointDestination(e))
	}
	return sum
}
