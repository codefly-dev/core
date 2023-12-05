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

func WithAPI(endpoint *configurations.Endpoint, source APISource) (*basev1.Endpoint, error) {
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

type APISource interface {
	Proto() (*basev1.API, error)
}

type GrpcAPI struct {
	filename string
	content  []byte
}

func NewGrpcAPI(endpoint *configurations.Endpoint, filename string) (*basev1.Endpoint, error) {
	// Read the file content
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %v", filename, err)
	}
	return WithAPI(endpoint, &GrpcAPI{filename: filename, content: content})
}

func (grpc *GrpcAPI) Proto() (*basev1.API, error) {
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

type RestAPI struct {
	// openapi []byte
}

func NewRestAPI(endpoint *configurations.Endpoint) (*basev1.Endpoint, error) {
	return WithAPI(endpoint, &RestAPI{})
}

func (HTTP *RestAPI) Proto() (*basev1.API, error) {
	restAPI := &basev1.RestAPI{}
	// Add an API message with the GrpcAPI message
	api := &basev1.API{
		Value: &basev1.API_Rest{
			Rest: restAPI,
		},
	}
	return api, nil
}

func NewRestAPIFromOpenAPI(ctx context.Context, endpoint *configurations.Endpoint, filename string) (*basev1.Endpoint, error) {
	logger := shared.GetBaseLogger(ctx).With("NewRestAPIFromOpenAPI")
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
	swagger, err := parseOpenAPI(content)
	if err != nil {
		return nil, logger.Wrapf(err, "failed to parse openapi spec")
	}

	rest.Rest.Openapi = content
	for path := range swagger.Paths.Paths {
		item := swagger.Paths.Paths[path]
		rest.Rest.Routes = append(rest.Rest.Routes, &basev1.RestRoute{
			Methods: getHTTPMethodsFromPathItem(&item),
			Path:    path,
		})
	}
	return e, nil
}

type TCP struct{}

func NewTCP() (*TCP, error) {
	return &TCP{}, nil
}

func (*TCP) Proto() (*basev1.API, error) {
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
func parseOpenAPI(spec []byte) (*openapispec.Swagger, error) {
	analyzed, err := openapiloads.Analyzed(spec, "2.0")
	if err != nil {
		return nil, err
	}
	return analyzed.Spec(), nil
}

func getHTTPMethodsFromPathItem(pathItem *openapispec.PathItem) []basev1.HTTPMethod {
	var methods []basev1.HTTPMethod

	if pathItem.Get != nil {
		methods = append(methods, basev1.HTTPMethod_GET)
	}
	if pathItem.Post != nil {
		methods = append(methods, basev1.HTTPMethod_POST)
	}
	if pathItem.Put != nil {
		methods = append(methods, basev1.HTTPMethod_PUT)
	}
	if pathItem.Delete != nil {
		methods = append(methods, basev1.HTTPMethod_DELETE)
	}
	if pathItem.Options != nil {
		methods = append(methods, basev1.HTTPMethod_OPTIONS)
	}
	if pathItem.Head != nil {
		methods = append(methods, basev1.HTTPMethod_HEAD)
	}
	if pathItem.Patch != nil {
		methods = append(methods, basev1.HTTPMethod_PATCH)
	}
	return methods
}
