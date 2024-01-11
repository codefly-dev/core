package configurations

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/yoheimuta/go-protoparser/v4"
	"github.com/yoheimuta/go-protoparser/v4/parser"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
	openapiloads "github.com/go-openapi/loads"
	openapispec "github.com/go-openapi/spec"
)

func WithAPI(ctx context.Context, endpoint *Endpoint, source APISource) (*basev0.Endpoint, error) {
	w := wool.Get(ctx).In("endpoints.WithAPI")
	api, err := source.Proto()
	if err != nil {
		return nil, w.Wrapf(err, "cannot create grpc api: %v")
	}
	base := EndpointBaseProto(endpoint)
	base.Api = api
	return base, nil
}

type APISource interface {
	Proto() (*basev0.API, error)
}

type GrpcAPI struct {
	filename string
	content  []byte
	rpcs     []*basev0.RPC
}

func NewGrpcAPI(ctx context.Context, endpoint *Endpoint, filename string) (*basev0.Endpoint, error) {
	// Read the file content
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read file %s: %v", filename, err)
	}

	got, err := protoparser.Parse(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("failed to parse proto file %s: %v", filename, err)
	}

	var rpcs []*basev0.RPC
	for _, element := range got.ProtoBody {
		if service, ok := element.(*parser.Service); ok {
			for _, inside := range service.ServiceBody {
				if rpc, ok := inside.(*parser.RPC); ok {
					rpcs = append(rpcs, &basev0.RPC{Name: rpc.RPCName})
				}
			}
		}
	}

	return WithAPI(ctx, endpoint, &GrpcAPI{filename: filename, content: content, rpcs: rpcs})
}

func (grpc *GrpcAPI) Proto() (*basev0.API, error) {
	// Add a GrpcAPI message with the file content
	grpcAPI := &basev0.GrpcAPI{
		Proto: grpc.content,
		Rpcs:  grpc.rpcs,
	}
	// Add an API message with the GrpcAPI message
	api := &basev0.API{
		Value: &basev0.API_Grpc{
			Grpc: grpcAPI,
		},
	}
	return api, nil
}

type RestAPI struct {
	filename string
	openapi  []byte
	routes   []*basev0.RestRoute
}

func NewRestAPI(ctx context.Context, endpoint *Endpoint) (*basev0.Endpoint, error) {
	return WithAPI(ctx, endpoint, &RestAPI{})
}

func (rest *RestAPI) Proto() (*basev0.API, error) {
	restAPI := &basev0.RestAPI{
		Openapi: rest.openapi,
		Routes:  rest.routes,
	}
	// Add an API message with the GrpcAPI message
	api := &basev0.API{
		Value: &basev0.API_Rest{
			Rest: restAPI,
		},
	}
	return api, nil
}

func NewRestAPIFromOpenAPI(ctx context.Context, endpoint *Endpoint, filename string) (*basev0.Endpoint, error) {
	w := wool.Get(ctx).In("endpoints.NewRestAPIFromOpenAPI")
	if !shared.FileExists(filename) {
		return nil, w.NewError("file does not exist: %s", filename)
	}
	// Read the file content
	content, err := os.ReadFile(filename)
	if err != nil {
		return nil, w.Wrapf(err, "failed to read file")
	}
	swagger, err := parseOpenAPI(content)
	if err != nil {
		return nil, w.Wrapf(err, "failed to parse openapi spec")
	}

	var routes []*basev0.RestRoute
	for path := range swagger.Paths.Paths {
		item := swagger.Paths.Paths[path]
		routes = append(routes, &basev0.RestRoute{
			Methods: getHTTPMethodsFromPathItem(&item),
			Path:    path,
		})
	}
	return WithAPI(ctx, endpoint, &RestAPI{openapi: content, routes: routes, filename: filename})
}

type HTTPAPI struct{}

func (h *HTTPAPI) Proto() (*basev0.API, error) {
	return &basev0.API{
		Value: &basev0.API_Http{
			Http: &basev0.HttpAPI{}},
	}, nil
}

func NewHTTPApi(ctx context.Context, endpoint *Endpoint) (*basev0.Endpoint, error) {
	return WithAPI(ctx, endpoint, &HTTPAPI{})
}

type TCP struct{}

func NewTCP() (*TCP, error) {
	return &TCP{}, nil
}

func (*TCP) Proto() (*basev0.API, error) {
	// Add a GrpcAPI message with the file content
	tcp := &basev0.TcpAPI{}
	// Add an API message with the GrpcAPI message
	api := &basev0.API{
		Value: &basev0.API_Tcp{
			Tcp: tcp,
		},
	}
	return api, nil
}

func NewTCPAPI(ctx context.Context, endpoint *Endpoint) (*basev0.Endpoint, error) {
	return WithAPI(ctx, endpoint, &TCP{})
}

/* Helpers */
func parseOpenAPI(spec []byte) (*openapispec.Swagger, error) {
	analyzed, err := openapiloads.Analyzed(spec, "2.0")
	if err != nil {
		return nil, err
	}
	return analyzed.Spec(), nil
}

func getHTTPMethodsFromPathItem(pathItem *openapispec.PathItem) []basev0.HTTPMethod {
	var methods []basev0.HTTPMethod

	if pathItem.Get != nil {
		methods = append(methods, basev0.HTTPMethod_GET)
	}
	if pathItem.Post != nil {
		methods = append(methods, basev0.HTTPMethod_POST)
	}
	if pathItem.Put != nil {
		methods = append(methods, basev0.HTTPMethod_PUT)
	}
	if pathItem.Delete != nil {
		methods = append(methods, basev0.HTTPMethod_DELETE)
	}
	if pathItem.Options != nil {
		methods = append(methods, basev0.HTTPMethod_OPTIONS)
	}
	if pathItem.Head != nil {
		methods = append(methods, basev0.HTTPMethod_HEAD)
	}
	if pathItem.Patch != nil {
		methods = append(methods, basev0.HTTPMethod_PATCH)
	}
	return methods
}
