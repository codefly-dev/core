package resources

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/codefly-dev/core/standards"
	"github.com/codefly-dev/core/wool"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

type Visibility = string

const (
	// VisibilityExternal represents an endpoint that exists outside the system
	VisibilityExternal Visibility = "external"
	// VisibilityPublic represents a deployed endpoint that is accessible from outside the system
	VisibilityPublic Visibility = "public"
	// VisibilityModule represents an endpoint from other modules inside the system
	VisibilityModule Visibility = "module"
	// VisibilityPrivate represents an endpoint that is only accessible within the module
	VisibilityPrivate Visibility = "private"
)

// Endpoint is the fundamental entity that standardize communication between services.
type Endpoint struct {
	Name        string `yaml:"name"`
	Service     string `yaml:"service,omitempty"`
	Module      string `yaml:"module,omitempty"`
	Description string `yaml:"description,omitempty"`
	Visibility  string `yaml:"visibility,omitempty"`
	API         string `yaml:"api,omitempty"`
}

func (endpoint *Endpoint) postLoad() {
	if endpoint.Visibility == "" {
		endpoint.Visibility = VisibilityPrivate
	}
	if endpoint.API == "" && slices.Contains(standards.APIS(), endpoint.Name) {
		endpoint.API = endpoint.Name
	}
}

func (endpoint *Endpoint) preSave() {
	if endpoint.Visibility == VisibilityPrivate {
		endpoint.Visibility = ""
	}
	if endpoint.API == endpoint.Name {
		endpoint.API = ""
	}
}

func (endpoint *Endpoint) Unique() string {
	unique := endpoint.ServiceUnique()
	unique += endpoint.Information().Identifier()
	return unique
}

func (endpoint *Endpoint) ServiceUnique() string {
	return ServiceUnique(endpoint.Module, endpoint.Service)
}

func ServiceUniqueFromEndpoint(endpoint *basev0.Endpoint) string {
	return ServiceUnique(endpoint.Module, endpoint.Service)
}

func (endpoint *EndpointInformation) UnknownAPI() bool {
	return endpoint.API == standards.Unknown || endpoint.API == ""
}

// Identifier satisfies this format:
// - name::api if name != api
// - api if name == api or name == ""
func (endpoint *EndpointInformation) Identifier() string {
	if endpoint.UnknownAPI() {
		if endpoint.Name == "" {
			return ""
		}
		return fmt.Sprintf("/%s", endpoint.Name)
	}
	if endpoint.Name == endpoint.API {
		return fmt.Sprintf("/%s", endpoint.API)
	}
	return fmt.Sprintf("/%s::%s", endpoint.Name, endpoint.API)
}

type EndpointInformation struct {
	Module  string
	Service string
	Name    string
	API     string
}

func EndpointInformationFromProto(endpoint *basev0.Endpoint) *EndpointInformation {
	return &EndpointInformation{
		Module:  endpoint.Module,
		Service: endpoint.Service,
		Name:    endpoint.Name,
		API:     endpoint.Api,
	}
}

// This is the format to override endpoints

func ParseEndpoint(unique string) (*EndpointInformation, error) {
	// Do we have the explicit APIva
	endpoint := &EndpointInformation{}
	if strings.Contains(unique, "::") {
		tokens := strings.Split(unique, "::")
		if len(tokens) != 2 {
			return nil, fmt.Errorf("info needs to be of the form app/svc/info::api")
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
	in, err := ParseServiceWithOptionalModule(unique)
	if err != nil {
		return nil, err
	}
	endpoint.Service = in.Name
	endpoint.Module = in.Module

	return endpoint, nil
}

func (endpoint *Endpoint) AsReference() *EndpointReference {
	return &EndpointReference{
		Name: endpoint.Name,
	}
}

func (endpoint *Endpoint) Proto() (*basev0.Endpoint, error) {
	if endpoint.API == "" && standards.IsSupportedAPI(endpoint.Name) == nil {
		endpoint.API = endpoint.Name
	}
	if err := standards.IsSupportedAPI(endpoint.API); err != nil {
		return nil, fmt.Errorf("unsupported api: %s", endpoint.API)
	}
	e := &basev0.Endpoint{
		Name:        endpoint.Name,
		Module:      endpoint.Module,
		Service:     endpoint.Service,
		Api:         endpoint.API,
		Visibility:  endpoint.Visibility,
		Description: endpoint.Description,
	}
	// Validate
	if err := Validate(e); err != nil {
		return nil, err
	}
	return e, nil
}

func (endpoint *Endpoint) Information() *EndpointInformation {
	return &EndpointInformation{
		Module:  endpoint.Module,
		Service: endpoint.Service,
		Name:    endpoint.Name,
		API:     endpoint.API,
	}
}

func EndpointFromProto(e *basev0.Endpoint) *Endpoint {
	return &Endpoint{
		Name:        e.Name,
		Module:      e.Module,
		Service:     e.Service,
		Visibility:  e.Visibility,
		Description: e.Description,
		API:         e.Api,
	}
}

func FromProtoEndpoints(es ...*basev0.Endpoint) ([]*Endpoint, error) {
	var endpoints []*Endpoint
	for _, e := range es {
		endpoints = append(endpoints, EndpointFromProto(e))
	}
	return endpoints, nil
}

func Light(e *basev0.Endpoint) *basev0.Endpoint {
	return &basev0.Endpoint{
		Name:        e.Name,
		Visibility:  e.Visibility,
		Description: e.Description,
		Api:         e.Api,
		ApiDetails:  LightAPI(e.ApiDetails),
	}
}

func IsRest(_ context.Context, endpoint *basev0.Endpoint) *basev0.RestAPI {
	if endpoint == nil {
		return nil
	}
	if endpoint.Api != standards.REST {
		return nil
	}
	switch v := endpoint.ApiDetails.Value.(type) {
	case *basev0.API_Rest:
		return v.Rest
	default:
		return nil
	}
}

func IsGRPC(_ context.Context, endpoint *basev0.Endpoint) *basev0.GrpcAPI {
	if endpoint == nil {
		return nil
	}
	if endpoint.Api != standards.GRPC {
		return nil
	}
	switch v := endpoint.ApiDetails.Value.(type) {
	case *basev0.API_Grpc:
		return v.Grpc
	default:
		return nil
	}
}

func IsHTTP(_ context.Context, endpoint *basev0.Endpoint) *basev0.HttpAPI {
	if endpoint == nil {
		return nil
	}
	if endpoint.Api != standards.HTTP {
		return nil
	}
	switch v := endpoint.ApiDetails.Value.(type) {
	case *basev0.API_Http:
		return v.Http
	default:
		return nil
	}
}

func IsTCP(_ context.Context, endpoint *basev0.Endpoint) *basev0.TcpAPI {
	if endpoint == nil {
		return nil
	}
	if endpoint.Api != standards.TCP {
		return nil
	}
	switch v := endpoint.ApiDetails.Value.(type) {
	case *basev0.API_Tcp:
		return v.Tcp
	default:
		return nil
	}
}

type EndpointSummary struct {
	Count   int
	Uniques []string
}

func MakeManyEndpointSummary(endpoints []*basev0.Endpoint) EndpointSummary {
	sum := EndpointSummary{}
	sum.Count = len(endpoints)
	for _, e := range endpoints {
		sum.Uniques = append(sum.Uniques, MakeEndpointSummary(e))
	}
	return sum
}

func MakeEndpointSummary(endpoint *basev0.Endpoint) string {
	if endpoint == nil {
		return "NIL"
	}
	return EndpointDestination(endpoint)
}

func EndpointDestination(e *basev0.Endpoint) string {
	return EndpointFromProto(e).Unique()
}

// Compute "change" of endpoints

func endpointHash(_ context.Context, endpoint *basev0.Endpoint) (string, error) {
	// w := wool.Get(ctx).In("configurations.EndpointHash")
	var buf bytes.Buffer
	buf.WriteString(endpoint.Name)
	buf.WriteString(endpoint.Visibility)
	buf.WriteString(endpoint.Api)
	buf.WriteString(endpoint.ApiDetails.String())
	// if rest := EndpointRestAPI(endpoint); rest != nil {
	//	w.Debug("hashing rest api TODO: more precise hashing", wool.NameField(endpoint.Name))
	//	buf.WriteString(rest.String())
	// }
	// if grpc := EndpointGRPCAPI(endpoint); grpc != nil {
	//	w.Debug("hashing grpc api", wool.NameField(endpoint.Name))
	//	buf.WriteString(grpc.String())
	// }
	return Hash(buf.Bytes()), nil
}

func EndpointHash(ctx context.Context, endpoints ...*basev0.Endpoint) (string, error) {
	w := wool.Get(ctx).In("configurations.EndpointsHash")
	hasher := NewHasher()
	for _, endpoint := range endpoints {
		hash, err := endpointHash(ctx, endpoint)
		if err != nil {
			return "", w.Wrapf(err, "cannot compute info hash")
		}
		hasher.Add(hash)
	}
	return hasher.Hash(), nil
}

func FindGRPCEndpoint(ctx context.Context, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	for _, e := range endpoints {
		if IsGRPC(ctx, e) != nil {
			return e, nil
		}
	}
	return nil, fmt.Errorf("no grpc endpoint found")
}

func FindRestEndpoint(ctx context.Context, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	for _, e := range endpoints {
		if IsRest(ctx, e) != nil {
			return e, nil
		}
	}
	return nil, fmt.Errorf("no rest endpoint found")
}

func FindHTTPEndpoint(ctx context.Context, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	for _, e := range endpoints {
		if IsHTTP(ctx, e) != nil {
			return e, nil
		}
	}
	return nil, fmt.Errorf("no http endpoint found")
}

func FindTCPEndpoint(ctx context.Context, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	for _, e := range endpoints {
		if IsTCP(ctx, e) != nil {
			return e, nil
		}
	}
	return nil, fmt.Errorf("no tcp endpoint found")
}

func FindTCPEndpointWithName(ctx context.Context, name string, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	for _, e := range endpoints {
		if e.Name != name {
			continue
		}
		if IsTCP(ctx, e) != nil {
			return e, nil
		}
	}
	return nil, fmt.Errorf("no tcp endpoint found")
}

func HasPublicEndpoints(endpoints []*basev0.Endpoint) bool {
	for _, endpoint := range endpoints {
		if endpoint.Visibility == VisibilityPublic {
			return true
		}
	}
	return false
}
