package configurations

import (
	"bytes"
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/codefly-dev/core/configurations/standards"
	"github.com/codefly-dev/core/wool"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type Visibility = string

const (
	VisibilityApplication Visibility = "application"
	VisibilityPublic      Visibility = "public"
)

// Endpoint is the fundamental entity that standardize communication between services.
type Endpoint struct {
	Name        string `yaml:"name,omitempty"`
	Service     string `yaml:"service,omitempty"`
	Application string `yaml:"application,omitempty"`
	Description string `yaml:"description,omitempty"`
	Visibility  string `yaml:"visibility,omitempty"`
	API         string `yaml:"api,omitempty"`
}

func (endpoint *Endpoint) Unique() string {
	unique := endpoint.ServiceUnique()
	unique += endpoint.Identifier()
	return unique
}

func (endpoint *Endpoint) ServiceUnique() string {
	return ServiceUnique(endpoint.Application, endpoint.Service)
}

func ServiceUniqueFromEndpoint(endpoint *basev0.Endpoint) string {
	return ServiceUnique(endpoint.Application, endpoint.Service)
}

func (endpoint *Endpoint) UnknownAPI() bool {
	return endpoint.API == Unknown || endpoint.API == ""
}

// Identifier satisfies this format:
// - name::api if name != api
// - api if name == api or name == ""
func (endpoint *Endpoint) Identifier() string {
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

func (endpoint *Endpoint) AsReference() *EndpointReference {
	return &EndpointReference{
		Name: endpoint.Name,
	}
}

/* For runtime */

const EndpointIdentifier = "ENDPOINT"

// TODO: want to encode only if we need to
func SerializeAddresses(addresses []string) string {
	return strings.Join(addresses, ",")
}

func DeserializeAddresses(data string) ([]string, error) {
	return strings.Split(string(data), ","), nil
}

func EndpointEnvironmentVariableKey(endpoint *Endpoint) string {
	key := IdentifierKey(EndpointIdentifier, endpoint.Application, endpoint.Service)
	id := strings.ToUpper(endpoint.Identifier())
	id = strings.Replace(id, "/", "___", 1)
	id = strings.Replace(id, "::", "____", 1)
	return fmt.Sprintf("%s%s", key, id)
}

func AsEndpointEnvironmentVariable(_ context.Context, endpoint *Endpoint, addresses []string) string {
	return fmt.Sprintf("%s=%s", EndpointEnvironmentVariableKey(endpoint), SerializeAddresses(addresses))
}

const Unknown = "unknown"
const NA = "NA"

type EndpointInstance struct {
	*Endpoint
	Addresses []string
}

func DefaultEndpointInstance(unique string) *EndpointInstance {
	// Try to figure out the API from unique
	endpoint, err := ParseEndpoint(unique)
	if err != nil {
		return &EndpointInstance{
			Addresses: []string{standards.StandardPortAddress(standards.TCP)},
		}
	}
	return &EndpointInstance{
		Addresses: []string{standards.StandardPortAddress(endpoint.API)},
	}
}

func (instance *EndpointInstance) Address() (string, error) {
	if len(instance.Addresses) != 1 {
		return "", fmt.Errorf("endpoint instance has more than one address")
	}
	return instance.Addresses[0], nil
}

func (instance *EndpointInstance) PortAddress() (string, error) {
	port, err := instance.Port()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(":%d", port), nil
}

func (instance *EndpointInstance) Port() (int, error) {
	address, err := instance.Address()
	if err != nil {
		return standards.StandardPort(standards.TCP), err
	}
	tokens := strings.Split(address, ":")
	if len(tokens) == 2 {
		return strconv.Atoi(tokens[1])
	}
	return standards.StandardPort(standards.TCP), fmt.Errorf("endpoint instance address does not have a port")
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
	return APIAsStandard(endpoint.Api)
}

func APIAsStandard(api *basev0.API) (string, error) {
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

func EndpointFromProto(e *basev0.Endpoint) *Endpoint {
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
		endpoints = append(endpoints, EndpointFromProto(e))
	}
	return endpoints, nil
}

func EndpointDestination(e *basev0.Endpoint) string {
	return fmt.Sprintf("%s/%s/%s::%s", e.Application, e.Service, e.Name, FromProtoAPI(e.Api))
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
				Rest: &basev0.RestAPI{Groups: api.Value.(*basev0.API_Rest).Rest.Groups},
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

type RouteUnique struct {
	service     string
	application string
	path        string
	method      HTTPMethod
}

func (r RouteUnique) String() string {
	return fmt.Sprintf("%s/%s%s[%s]", r.application, r.service, r.path, r.method)
}

func DetectNewRoutesFromEndpoints(ctx context.Context, endpoints []*basev0.Endpoint, known []*RestRouteGroup) []*RestRouteGroup {
	w := wool.Get(ctx).In("DetectNewRoutes")
	knownRoutes := make(map[string]bool)
	for _, k := range known {
		for _, r := range k.Routes {
			w.Debug("adding route", wool.Field("route", r))
			u := RouteUnique{
				service:     k.Service,
				application: k.Application,
				path:        r.Path,
				method:      r.Method,
			}
			knownRoutes[u.String()] = true
		}
	}
	w.Debug("known routes", wool.Field("count", len(knownRoutes)))
	newGroups := make(map[string]*RestRouteGroup)

	for _, e := range endpoints {
		if rest := IsRest(ctx, e.Api); rest != nil {
			w.Debug("found a REST API", wool.NameField(e.Name))
			for _, group := range rest.Groups {
				for _, r := range group.Routes {
					w.Focus("route", wool.Field("route", e.Application))
					key := RouteUnique{
						service:     e.Service,
						application: e.Application,
						path:        r.Path,
						method:      ConvertMethod(r.Method),
					}
					if _, ok := knownRoutes[key.String()]; !ok {
						w.Debug("detected unknown route", wool.Field("route", key.String()))
						var outputGroup *RestRouteGroup
						var ok bool
						if outputGroup, ok = newGroups[group.String()]; !ok {
							outputGroup = &RestRouteGroup{
								Application: e.Application,
								Service:     e.Service,
								Path:        r.Path,
							}
							newGroups[group.String()] = outputGroup
						}
						outputGroup.Routes = append(outputGroup.Routes, RestRouteFromProto(r))
					}
				}
			}
		}
	}
	var output []*RestRouteGroup
	for _, g := range newGroups {
		output = append(output, g)
	}
	return output
}

func DetectNewGRPCRoutesFromEndpoints(ctx context.Context, endpoints []*basev0.Endpoint, known []*GRPCRoute) []*GRPCRoute {
	w := wool.Get(ctx).In("DetectNewGRPCRoutes")
	knownRoutes := make(map[string]bool)
	for _, k := range known {
		knownRoutes[k.Name] = true
	}
	var newRoutes []*GRPCRoute
	for _, e := range endpoints {
		if grpc := IsGRPC(ctx, e.Api); grpc != nil {
			for _, rpc := range grpc.Rpcs {
				w.Debug("found a GRPC API", wool.NameField(rpc.Name))
				if _, ok := knownRoutes[rpc.Name]; !ok {
					w.Debug("detected unknown RPC", wool.NameField(rpc.Name))
					newRoutes = append(newRoutes, GRPCRouteFromProto(e, grpc, rpc))
				}
			}
		}
	}
	return newRoutes
}

// FindEndpointForRestRoute finds the endpoint that matches the route rpcs
func FindEndpointForRestRoute(ctx context.Context, endpoints []*basev0.Endpoint, route *RestRouteGroup) *basev0.Endpoint {
	for _, e := range endpoints {
		if e.Application == route.Application && e.Service == route.Service && IsRest(ctx, e.Api) != nil {
			return e
		}
	}
	return nil
}

// FindEndpointForGRPCRoute finds the endpoint that matches the route rpcs
func FindEndpointForGRPCRoute(ctx context.Context, endpoints []*basev0.Endpoint, route *GRPCRoute) *basev0.Endpoint {
	for _, e := range endpoints {
		if e.Application == route.Application && e.Service == route.Service && IsGRPC(ctx, e.Api) != nil {
			return e
		}
	}
	return nil
}

func IsRest(_ context.Context, api *basev0.API) *basev0.RestAPI {
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

func IsGRPC(_ context.Context, api *basev0.API) *basev0.GrpcAPI {
	if api == nil {
		return nil
	}
	switch v := api.Value.(type) {
	case *basev0.API_Grpc:
		return v.Grpc
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

// Compute "change" of endpoints

func endpointHash(ctx context.Context, endpoint *basev0.Endpoint) (string, error) {
	w := wool.Get(ctx).In("configurations.EndpointHash")
	var buf bytes.Buffer
	buf.WriteString(endpoint.Name)
	buf.WriteString(endpoint.Visibility)
	buf.WriteString(endpoint.Api.String())
	if rest := EndpointRestAPI(endpoint); rest != nil {
		w.Debug("hashing rest api TODO: more precise hashing", wool.NameField(endpoint.Name))
		buf.WriteString(rest.String())
	}
	if grpc := EndpointGRPCAPI(endpoint); grpc != nil {
		w.Debug("hashing grpc api", wool.NameField(endpoint.Name))
		buf.WriteString(grpc.String())
	}
	return Hash(buf.Bytes()), nil
}

func EndpointHash(ctx context.Context, endpoints ...*basev0.Endpoint) (string, error) {
	w := wool.Get(ctx).In("configurations.EndpointsHash")
	hasher := NewHasher()
	for _, endpoint := range endpoints {
		hash, err := endpointHash(ctx, endpoint)
		if err != nil {
			return "", w.Wrapf(err, "cannot compute endpoint hash")
		}
		hasher.Add(hash)
	}
	return hasher.Hash(), nil
}

func CloneEndpoint(_ context.Context, endpoint *basev0.Endpoint) *basev0.Endpoint {
	return &basev0.Endpoint{
		Name:        endpoint.Name,
		Application: endpoint.Application,
		Service:     endpoint.Service,
		Visibility:  endpoint.Visibility,
		Description: endpoint.Description,
		Api:         CloneAPI(endpoint.Api),
	}

}

func CloneAPI(api *basev0.API) *basev0.API {
	if api == nil {
		return nil
	}
	switch v := api.Value.(type) {
	case *basev0.API_Grpc:
		return &basev0.API{
			Value: &basev0.API_Grpc{},
		}
	case *basev0.API_Rest:
		return &basev0.API{
			Value: &basev0.API_Rest{
				Rest: &basev0.RestAPI{Groups: v.Rest.Groups},
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
