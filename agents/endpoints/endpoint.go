package endpoints

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/configurations"
	basev1 "github.com/codefly-dev/core/proto/v1/go/base"
)

type NilApiError struct {
	name string
}

func (err *NilApiError) Error() string {
	return fmt.Sprintf("endpoint <%s> api is nil", err.name)
}

type UnknownApiError struct {
	api *basev1.API
}

func (err *UnknownApiError) Error() string {
	return fmt.Sprintf("unknow api: <%v>", err.api)
}

func WhichApiFromEndpoint(endpoint *basev1.Endpoint) (string, error) {
	if endpoint.Api == nil {
		return "", &NilApiError{name: endpoint.Name}
	}
	return WhichApi(endpoint.Api)
}

func WhichApi(api *basev1.API) (string, error) {
	switch api.Value.(type) {
	case *basev1.API_Grpc:
		return configurations.Grpc, nil
	case *basev1.API_Rest:
		return configurations.Rest, nil
	case *basev1.API_Tcp:
		return configurations.Tcp, nil
	default:
		return "", &UnknownApiError{api}
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
		return 0, &UnknownApiError{api}
	}
}

type NilEndpointError struct{}

func (n NilEndpointError) Error() string {
	return "endpoint is nil"
}

func FromProtoEndpoint(e *basev1.Endpoint) (*configurations.Endpoint, error) {
	if e == nil {
		return nil, &NilEndpointError{}
	}
	return &configurations.Endpoint{
		Name:        e.Name,
		Scope:       e.Scope,
		Description: e.Description,
		Api:         FromProtoApi(e.Api),
	}, nil
}

func Destination(e *basev1.Endpoint) string {
	return fmt.Sprintf("%s/%s/%s[%s]", e.Application, e.Service, e.Name, FromProtoApi(e.Api))
}

func FromProtoApi(api *basev1.API) string {
	if api == nil {
		return configurations.Unknown
	}
	switch api.Value.(type) {
	case *basev1.API_Grpc:
		return configurations.Grpc
	case *basev1.API_Rest:
		return configurations.Rest
	case *basev1.API_Tcp:
		return configurations.Tcp
	default:
		return configurations.Unknown
	}
}

func LightApi(api *basev1.API) *basev1.API {
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
		Scope:       e.Scope,
		Description: e.Description,
		Api:         e.Api,
	}
}

func FlattenEndpoints(ctx context.Context, group *basev1.EndpointGroup) []*basev1.Endpoint {
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

func DetectNewRoutes(ctx context.Context, known []*configurations.RestRoute, group *basev1.EndpointGroup) []*configurations.RestRoute {
	logger := ctx.Value(shared.Agent).(shared.BaseLogger)
	if group == nil {
		logger.Debugf("we have a nil group")
		return nil
	}
	logger.Debugf("application groups: #%d", len(group.ApplicationEndpointGroup))
	var newRoutes []*configurations.RestRoute
	for _, app := range group.ApplicationEndpointGroup {
		logger.DebugMe("service groups: %s #%d", app.Name, len(app.ServiceEndpointGroups))
		for _, svc := range app.ServiceEndpointGroups {
			logger.DebugMe("endpoints: %s #%d", svc.Name, len(svc.Endpoints))
			for _, ep := range svc.Endpoints {
				if rest := IsRest(ctx, ep.Api); rest != nil {
					for _, route := range rest.Routes {
						potential := &configurations.RestRoute{
							Application: app.Name,
							Service:     svc.Name,
							Path:        route.Path,
							Methods:     configurations.ConvertMethods(route.Methods),
						}
						if !configurations.ContainsRoute(known, potential) {
							newRoutes = append(newRoutes, potential)
						}
					}
				}
			}
		}
	}
	return newRoutes
}

func IsRest(ctx context.Context, api *basev1.API) *basev1.RestAPI {
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
