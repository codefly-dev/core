package endpoints

import (
	"bytes"
	"context"
	"fmt"
	"github.com/yoheimuta/go-protoparser/v4"
	"github.com/yoheimuta/go-protoparser/v4/parser"
	"os"

	"github.com/codefly-dev/core/configurations"
	basev1 "github.com/codefly-dev/core/generated/v1/go/proto/base"
	"github.com/codefly-dev/core/shared"
	openapiloads "github.com/go-openapi/loads"
	openapispec "github.com/go-openapi/spec"
)

func WithAPI(endpoint *configurations.Endpoint, source APISource) (*basev1.Endpoint, error) {
	logger := shared.NewLogger().With("services.DefaultApi")
	api, err := source.Proto()
	if err != nil {
		return nil, logger.Wrapf(err, "cannot create grpc api: %v")
	}
	base := BaseProto(endpoint)
	base.Api = api
	return base, nil
}

type APISource interface {
	Proto() (*basev1.API, error)
}

type GrpcAPI struct {
	filename string
	content  []byte
	rpcs     []*basev1.RPC
}

func NewGrpcAPI(endpoint *configurations.Endpoint, filename string) (*basev1.Endpoint, error) {
	// Read the file content
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %v", filename, err)
	}

	got, err := protoparser.Parse(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("failed to parse proto file %s: %v", filename, err)
	}

	var rpcs []*basev1.RPC
	for _, element := range got.ProtoBody {
		if service, ok := element.(*parser.Service); ok {
			for _, inside := range service.ServiceBody {
				if rpc, ok := inside.(*parser.RPC); ok {
					rpcs = append(rpcs, &basev1.RPC{Name: rpc.RPCName})
				}
			}
		}
	}

	return WithAPI(endpoint, &GrpcAPI{filename: filename, content: content, rpcs: rpcs})
}

func (grpc *GrpcAPI) Proto() (*basev1.API, error) {
	// Add a GrpcAPI message with the file content
	grpcAPI := &basev1.GrpcAPI{
		Proto: grpc.content,
		Rpcs:  grpc.rpcs,
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
	filename string
	openapi  []byte
	routes   []*basev1.RestRoute
}

func NewRestAPI(endpoint *configurations.Endpoint) (*basev1.Endpoint, error) {
	return WithAPI(endpoint, &RestAPI{})
}

func (rest *RestAPI) Proto() (*basev1.API, error) {
	restAPI := &basev1.RestAPI{
		Openapi: rest.openapi,
		Routes:  rest.routes,
	}
	// Add an API message with the GrpcAPI message
	api := &basev1.API{
		Value: &basev1.API_Rest{
			Rest: restAPI,
		},
	}
	return api, nil
}

func NewRestAPIFromOpenAPI(ctx context.Context, endpoint *configurations.Endpoint, filename string) (*basev1.Endpoint, error) {
	logger := shared.GetLogger(ctx).With("NewRestAPIFromOpenAPI")
	logger.TODO("visibility")
	if !shared.FileExists(filename) {
		return nil, logger.Errorf("file does not exist: %s", filename)
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

	var routes []*basev1.RestRoute
	for path := range swagger.Paths.Paths {
		item := swagger.Paths.Paths[path]
		routes = append(routes, &basev1.RestRoute{
			Methods: getHTTPMethodsFromPathItem(&item),
			Path:    path,
		})
	}
	return WithAPI(endpoint, &RestAPI{openapi: content, routes: routes, filename: filename})
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
