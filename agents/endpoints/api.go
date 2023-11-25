package endpoints

import (
	"context"
	"fmt"
	"os"

	"github.com/codefly-dev/core/configurations"
	basev1 "github.com/codefly-dev/core/proto/v1/go/base"
	"github.com/codefly-dev/core/shared"
	openapiloads "github.com/go-openapi/loads"
	openapispec "github.com/go-openapi/spec"
)

func WithApi(endpoint *configurations.Endpoint, source ApiSource) (*basev1.Endpoint, error) {
	logger := shared.NewLogger("services.DefaultApi")
	logger.Debugf("VISILIBITY %v", endpoint.Scope)
	api, err := source.Proto()
	if err != nil {
		return nil, logger.Wrapf(err, "cannot create grpc api: %v")
	}
	return &basev1.Endpoint{
		Name:        endpoint.Name,
		Scope:       endpoint.Scope,
		Description: endpoint.Description,
		Api:         api,
	}, nil
}

type ApiSource interface {
	Proto() (*basev1.API, error)
}

type GrpcApi struct {
	filename string
	content  []byte
}

func NewGrpcApi(endpoint *configurations.Endpoint, filename string) (*basev1.Endpoint, error) {
	// Read the file content
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %v", filename, err)
	}
	return WithApi(endpoint, &GrpcApi{filename: filename, content: content})
}

func (grpc *GrpcApi) Proto() (*basev1.API, error) {
	// Add a GrpcAPI message with the file content
	grpcAPI := &basev1.GrpcAPI{
		Proto: grpc.content,
	}
	// Add an API message with the GrpcAPI message
	api := &basev1.API{
		Value: &basev1.API_Grpc{
			Grpc: grpcAPI,
		},
	}
	return api, nil
}

type RestApi struct {
	// openapi []byte
}

func NewRestApi(endpoint *configurations.Endpoint) (*basev1.Endpoint, error) {
	return WithApi(endpoint, &RestApi{})
}

func (http *RestApi) Proto() (*basev1.API, error) {
	restAPI := &basev1.RestAPI{}
	// Add an API message with the GrpcAPI message
	api := &basev1.API{
		Value: &basev1.API_Rest{
			Rest: restAPI,
		},
	}
	return api, nil
}

func NewRestApiFromOpenAPI(ctx context.Context, endpoint *configurations.Endpoint, filename string) (*basev1.Endpoint, error) {
	logger := shared.NewLogger("services.Default")
	logger.TODO("visibility")
	rest := &basev1.API_Rest{Rest: &basev1.RestAPI{}}
	e := &basev1.Endpoint{
		Name:        endpoint.Name,
		Scope:       endpoint.Scope,
		Description: endpoint.Description,
		Api: &basev1.API{
			Value: rest,
		},
	}
	if !shared.FileExists(filename) {
		return e, nil
	}
	// Read the file content
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, logger.Wrapf(err, "failed to read file")
	}
	swagger, err := parseOpenApi(content)
	if err != nil {
		return nil, logger.Wrapf(err, "failed to parse openapi spec")
	}

	rest.Rest.Openapi = content
	for p, item := range swagger.Paths.Paths {
		rest.Rest.Routes = append(rest.Rest.Routes, &basev1.RestRoute{
			Methods: getHTTPMethodsFromPathItem(item),
			Path:    p,
		})
	}
	return e, nil
}

type Tcp struct{}

func NewTcp() (*Tcp, error) {
	return &Tcp{}, nil
}

func (*Tcp) Proto() (*basev1.API, error) {
	// Add a GrpcAPI message with the file content
	tcp := &basev1.TcpAPI{}
	// Add an API message with the GrpcAPI message
	api := &basev1.API{
		Value: &basev1.API_Tcp{
			Tcp: tcp,
		},
	}
	return api, nil
}

/* Helpers */
func parseOpenApi(spec []byte) (*openapispec.Swagger, error) {
	analyzed, err := openapiloads.Analyzed(spec, "2.0")
	if err != nil {
		return nil, err
	}
	return analyzed.Spec(), nil
}

func getHTTPMethodsFromPathItem(pathItem openapispec.PathItem) []basev1.HttpMethod {
	var methods []basev1.HttpMethod

	if pathItem.Get != nil {
		methods = append(methods, basev1.HttpMethod_GET)
	}
	if pathItem.Post != nil {
		methods = append(methods, basev1.HttpMethod_POST)
	}
	if pathItem.Put != nil {
		methods = append(methods, basev1.HttpMethod_PUT)
	}
	if pathItem.Delete != nil {
		methods = append(methods, basev1.HttpMethod_DELETE)
	}
	if pathItem.Options != nil {
		methods = append(methods, basev1.HttpMethod_OPTIONS)
	}
	if pathItem.Head != nil {
		methods = append(methods, basev1.HttpMethod_HEAD)
	}
	if pathItem.Patch != nil {
		methods = append(methods, basev1.HttpMethod_PATCH)
	}
	return methods
}
