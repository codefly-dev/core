package configurations

import (
	"context"
	"fmt"
	"strings"

	"github.com/codefly-dev/core/configurations/standards"

	basev1 "github.com/codefly-dev/core/generated/go/base/v1"
	"github.com/codefly-dev/core/wool"
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

func AsEndpointEnvironmentVariableKey(endpoint *Endpoint) string {
	unique := endpoint.Unique()
	unique = strings.ToUpper(unique)
	unique = strings.Replace(unique, "/", "__", 1)
	unique = strings.Replace(unique, "/", "___", 1)
	unique = strings.Replace(unique, "::", "____", 1)
	return strings.ToUpper(fmt.Sprintf("%s%s", EndpointPrefix, unique))
}

func AsEndpointEnvironmentVariable(endpoint *Endpoint, addresses []string) string {
	return fmt.Sprintf("%s=%s", AsEndpointEnvironmentVariableKey(endpoint), SerializeAddresses(addresses))
}

const Unknown = "unknown"

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

type NilAPIError struct {
	name string
}

func (err *NilAPIError) Error() string {
	return fmt.Sprintf("endpoint <%s> api is nil", err.name)
}

type UnknownAPIError struct {
	api *basev1.API
}

func (err *UnknownAPIError) Error() string {
	return fmt.Sprintf("unknow api: <%v>", err.api)
}

func WhichAPIFromEndpoint(endpoint *basev1.Endpoint) (string, error) {
	if endpoint.Api == nil {
		return "", &NilAPIError{name: endpoint.Name}
	}
	return WhichAPI(endpoint.Api)
}

func WhichAPI(api *basev1.API) (string, error) {
	switch api.Value.(type) {
	case *basev1.API_Grpc:
		return standards.GRPC, nil
	case *basev1.API_Rest:
		return standards.REST, nil
	case *basev1.API_Tcp:
		return standards.TCP, nil
	default:
		return "", &UnknownAPIError{api}
	}
}

func StandardPort(api *basev1.API) (int, error) {
	switch api.Value.(type) {
	case *basev1.API_Grpc:
		return 9090, nil
	case *basev1.API_Rest:
		return 8080, nil
	case *basev1.API_Tcp:
		return 7070, nil
	default:
		return 0, &UnknownAPIError{api}
	}
}

type NilEndpointError struct{}

func (n NilEndpointError) Error() string {
	return "endpoint is nil"
}

func EndpointBaseProto(e *Endpoint) *basev1.Endpoint {
	return &basev1.Endpoint{
		Name:        e.Name,
		Application: e.Application,
		Service:     e.Service,
		Visibility:  e.Visibility,
		Description: e.Description,
	}
}

func FromProtoEndpoint(e *basev1.Endpoint) (*Endpoint, error) {
	if e == nil {
		return nil, &NilEndpointError{}
	}
	return &Endpoint{
		Name:        e.Name,
		Application: e.Application,
		Service:     e.Service,
		Visibility:  e.Visibility,
		Description: e.Description,
		API:         FromProtoAPI(e.Api),
	}, nil
}

func FromProtoEndpoints(es ...*basev1.Endpoint) ([]*Endpoint, error) {
	var endpoints []*Endpoint
	for _, e := range es {
		endpoint, err := FromProtoEndpoint(e)
		if err != nil {
			return nil, err
		}
		endpoints = append(endpoints, endpoint)
	}
	return endpoints, nil
}

func Destination(e *basev1.Endpoint) string {
	return fmt.Sprintf("%s/%s/%s[%s]", e.Application, e.Service, e.Name, FromProtoAPI(e.Api))
}

func FromProtoAPI(api *basev1.API) string {
	if api == nil {
		return Unknown
	}
	switch api.Value.(type) {
	case *basev1.API_Grpc:
		return standards.GRPC
	case *basev1.API_Rest:
		return standards.REST
	case *basev1.API_Tcp:
		return standards.TCP
	default:
		return Unknown
	}
}

func LightAPI(api *basev1.API) *basev1.API {
	switch api.Value.(type) {
	case *basev1.API_Grpc:
		return &basev1.API{
			Value: &basev1.API_Grpc{},
		}
	case *basev1.API_Rest:
		return &basev1.API{
			Value: &basev1.API_Rest{
				Rest: &basev1.RestAPI{Routes: api.Value.(*basev1.API_Rest).Rest.Routes},
			},
		}
	case *basev1.API_Tcp:
		return &basev1.API{
			Value: &basev1.API_Tcp{},
		}
	default:
		return nil
	}
}

func Light(e *basev1.Endpoint) *basev1.Endpoint {
	return &basev1.Endpoint{
		Name:        e.Name,
		Visibility:  e.Visibility,
		Description: e.Description,
		Api:         e.Api,
	}
}

func FlattenEndpoints(_ context.Context, group *basev1.EndpointGroup) []*basev1.Endpoint {
	var endpoints []*basev1.Endpoint
	if group == nil {
		return endpoints
	}
	for _, app := range group.ApplicationEndpointGroup {
		for _, svc := range app.ServiceEndpointGroups {
			endpoints = append(endpoints, svc.Endpoints...)
		}
	}
	return endpoints
}

func FlattenRestRoutes(ctx context.Context, group *basev1.EndpointGroup) []*basev1.RestRoute {
	endpoints := FlattenEndpoints(ctx, group)
	var routes []*basev1.RestRoute
	for _, ep := range endpoints {
		if rest := ep.Api.GetRest(); rest != nil {
			routes = append(routes, rest.Routes...)
		}
	}
	return routes
}

func DetectNewRoutesFromGroup(ctx context.Context, known []*RestRoute, group *basev1.EndpointGroup) []*RestRoute {
	w := wool.Get(ctx).In("DetectNewRoutes")
	if group == nil {
		w.Debug("we have a nil group")
		return nil
	}
	endpoints := FlattenEndpoints(ctx, group)
	for _, e := range endpoints {
		w.Error("do", wool.Field("endpoint", e))
	}

	var newRoutes []*RestRoute
	for _, app := range group.ApplicationEndpointGroup {
		for _, svc := range app.ServiceEndpointGroups {
			for _, ep := range svc.Endpoints {
				if rest := HasRest(ctx, ep.Api); rest != nil {
					for _, route := range rest.Routes {
						potential := &RestRoute{
							Application: app.Name,
							Service:     svc.Name,
							Path:        route.Path,
							Methods:     ConvertMethods(route.Methods),
						}
						if !ContainsRoute(known, potential) {
							newRoutes = append(newRoutes, potential)
						}
					}
				}
			}
		}
	}
	return newRoutes
}

func FindEndpointForRoute(ctx context.Context, endpoints []*basev1.Endpoint, route *RestRoute) *basev1.Endpoint {
	for _, e := range endpoints {
		if e.Application == route.Application && e.Service == route.Service && HasRest(ctx, e.Api) != nil {
			return e
		}
	}
	return nil
}

func HasRest(_ context.Context, api *basev1.API) *basev1.RestAPI {
	if api == nil {
		return nil
	}
	switch v := api.Value.(type) {
	case *basev1.API_Rest:
		return v.Rest
	default:
		return nil
	}
}

func CondensedOutput(group *basev1.EndpointGroup) []string {
	if group == nil {
		return nil
	}
	var outs []string
	for _, appGroup := range group.ApplicationEndpointGroup {
		for _, svcGroup := range appGroup.ServiceEndpointGroups {
			if len(svcGroup.Endpoints) > 0 {
				outs = append(outs, fmt.Sprintf("%s/%s[#%d]", appGroup.Name, svcGroup.Name, len(svcGroup.Endpoints)))
				for _, e := range svcGroup.Endpoints {
					outs = append(outs, fmt.Sprintf("--%s", Destination(e)))
				}
			}
		}
	}
	return outs
}

func Condensed(es []*basev1.Endpoint) []string {
	var outs []string
	for _, e := range es {
		outs = append(outs, Destination(e))
	}
	return outs
}
