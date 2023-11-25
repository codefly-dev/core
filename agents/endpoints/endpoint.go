package endpoints

import (
	"fmt"

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
