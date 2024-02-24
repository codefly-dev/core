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
	base.Replicas = 1
	return base, nil
}

type APISource interface {
	Proto() (*basev0.API, error)
}

type GrpcAPI struct {
	filename    string
	content     []byte
	packageName string
	rpcs        []*basev0.RPC
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
	var packageName string
	for _, element := range got.ProtoBody {
		switch element := element.(type) {
		case *parser.Package:
			packageName = element.Name
		case *parser.Service:
			for _, inside := range element.ServiceBody {
				if rpc, ok := inside.(*parser.RPC); ok {
					rpcs = append(rpcs, &basev0.RPC{Name: rpc.RPCName, ServiceName: element.ServiceName})
				}
			}
		}
	}
	return WithAPI(ctx, endpoint, &GrpcAPI{filename: filename, content: content, packageName: packageName, rpcs: rpcs})
}

func (grpc *GrpcAPI) Proto() (*basev0.API, error) {
	// Add a GrpcAPI message with the file content
	grpcAPI := &basev0.GrpcAPI{
		Proto:   grpc.content,
		Rpcs:    grpc.rpcs,
		Package: grpc.packageName,
	}
	// Add an API message with the GrpcAPI message
	api := &basev0.API{
		Value: &basev0.API_Grpc{
			Grpc: grpcAPI,
		},
	}
	return api, nil
}

func EndpointGRPCAPI(endpoint *basev0.Endpoint) *basev0.GrpcAPI {
	if endpoint == nil {
		return nil
	}
	switch v := endpoint.Api.Value.(type) {
	case *basev0.API_Grpc:
		return v.Grpc
	default:
		return nil
	}
}

type RestAPI struct {
	filename string
	openapi  []byte
	groups   []*basev0.RestRouteGroup
}

func NewRestAPI(ctx context.Context, endpoint *Endpoint) (*basev0.Endpoint, error) {
	return WithAPI(ctx, endpoint, &RestAPI{})
}

func (rest *RestAPI) Proto() (*basev0.API, error) {
	restAPI := &basev0.RestAPI{
		Openapi: rest.openapi,
		Groups:  rest.groups,
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
	swagger, err := ParseOpenAPI(content)
	if err != nil {
		return nil, w.Wrapf(err, "failed to parse openapi spec")
	}

	groupMap := make(map[string]*basev0.RestRouteGroup)
	for path := range swagger.Paths.Paths {
		var group *basev0.RestRouteGroup
		var ok bool
		if group, ok = groupMap[path]; !ok {
			group = &basev0.RestRouteGroup{}
			groupMap[path] = group
		}
		item := swagger.Paths.Paths[path]
		methods := getHTTPMethodsFromPathItem(&item)
		for _, method := range methods {
			route := &basev0.RestRoute{Path: path, Method: method}
			group.Routes = append(group.Routes, route)
		}
	}
	var groups []*basev0.RestRouteGroup
	for _, group := range groupMap {
		groups = append(groups, group)
	}
	return WithAPI(ctx, endpoint, &RestAPI{openapi: content, groups: groups, filename: filename})
}

func EndpointRestAPI(endpoint *basev0.Endpoint) *basev0.RestAPI {
	if endpoint == nil {
		return nil
	}
	switch v := endpoint.Api.Value.(type) {
	case *basev0.API_Rest:
		return v.Rest
	default:
		return nil
	}
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

func ParseOpenAPI(spec []byte) (*openapispec.Swagger, error) {
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
