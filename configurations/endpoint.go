package configurations

import (
	"bytes"
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/codefly-dev/core/configurations/standards"
	"github.com/codefly-dev/core/wool"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

type Visibility = string

const (
	// VisibilityPublic represents an info that is externally visible
	VisibilityPublic Visibility = "public"
	// VisibilityApplication represents an application info: accessible from other applications
	VisibilityApplication Visibility = "application"
	// VisibilityPrivate represents an info that is only accessible within the application
	VisibilityPrivate Visibility = "private"
)

// Endpoint is the fundamental entity that standardize communication between services.
type Endpoint struct {
	Name        string `yaml:"name"`
	Service     string `yaml:"service,omitempty"`
	Application string `yaml:"application,omitempty"`
	Description string `yaml:"description,omitempty"`
	Visibility  string `yaml:"visibility"`
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
	return ServiceUnique(endpoint.Application, endpoint.Service)
}

func ServiceUniqueFromEndpoint(endpoint *basev0.Endpoint) string {
	return ServiceUnique(endpoint.Application, endpoint.Service)
}

func (endpoint *EndpointInformation) UnknownAPI() bool {
	return endpoint.API == Unknown || endpoint.API == ""
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
	Application string
	Service     string
	Name        string
	API         string
}

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

func (endpoint *Endpoint) Proto() (*basev0.Endpoint, error) {
	if err := standards.IsSupportedAPI(endpoint.API); err != nil {
		return nil, fmt.Errorf("unsupported api: %s", endpoint.API)
	}
	e := &basev0.Endpoint{
		Name:        endpoint.Name,
		Application: endpoint.Application,
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
		Application: endpoint.Application,
		Service:     endpoint.Service,
		Name:        endpoint.Name,
		API:         endpoint.API,
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

func EndpointEnvironmentVariableKey(endpoint *EndpointInformation) string {
	key := IdentifierKey(EndpointIdentifier, endpoint.Application, endpoint.Service)
	id := strings.ToUpper(endpoint.Identifier())
	id = strings.Replace(id, "/", "___", 1)
	id = strings.Replace(id, "::", "____", 1)
	return fmt.Sprintf("%s%s", key, id)
}

func AsEndpointEnvironmentVariable(_ context.Context, endpoint *Endpoint, address string) string {
	return fmt.Sprintf("%s=%s", EndpointEnvironmentVariableKey(endpoint.Information()), address)
}

const Unknown = "unknown"
const NA = "NA"

type EndpointInstance struct {
	*Endpoint
	Address     string
	PortAddress string
	Port        int
}

func (instance *EndpointInstance) WithAddress(address string) error {
	instance.Address = address
	port, portAddress, err := PortAndPortAddressFromAddress(instance.Address)
	if err != nil {
		return err
	}
	instance.PortAddress = portAddress
	instance.Port = port
	return nil
}

func DefaultEndpointInstance(unique string) (*EndpointInstance, error) {
	// Try to figure out the API from unique
	endpoint, err := ParseEndpoint(unique)
	if err != nil {
		return &EndpointInstance{
			Address: standards.PortAddress(standards.TCP),
		}, err
	}
	instance := &EndpointInstance{}
	err = instance.WithAddress(standards.LocalhostAddress(endpoint.API))
	return instance, err
}

type NilAPIError struct {
	name string
}

func (err *NilAPIError) Error() string {
	return fmt.Sprintf("info <%s> api is nil", err.name)
}

type UnknownAPIError struct {
	api *basev0.API
}

func (err *UnknownAPIError) Error() string {
	return fmt.Sprintf("unknow api: <%v>", err.api)
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

func Port(api *basev0.API) (int32, error) {
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
	return "info is nil"
}

func EndpointFromProto(e *basev0.Endpoint) *Endpoint {
	return &Endpoint{
		Name:        e.Name,
		Application: e.Application,
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

//
//	e
//	if e.Name == e.Api {
//		return fmt.Sprintf("%s/%s/%s", e.Application, e.Service, e.Name)
//	}
//	return fmt.Sprintf("%s/%s/%s::%s", e.Application, e.Service, e.Name, e.Api)
//}

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
	//w := wool.Get(ctx).In("configurations.EndpointHash")
	var buf bytes.Buffer
	buf.WriteString(endpoint.Name)
	buf.WriteString(endpoint.Visibility)
	buf.WriteString(endpoint.Api)
	buf.WriteString(endpoint.ApiDetails.String())
	//if rest := EndpointRestAPI(endpoint); rest != nil {
	//	w.Debug("hashing rest api TODO: more precise hashing", wool.NameField(endpoint.Name))
	//	buf.WriteString(rest.String())
	//}
	//if grpc := EndpointGRPCAPI(endpoint); grpc != nil {
	//	w.Debug("hashing grpc api", wool.NameField(endpoint.Name))
	//	buf.WriteString(grpc.String())
	//}
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

//
//func CloneEndpoint(_ context.Context, endpoint *basev0.Endpoint) *basev0.Endpoint {
//	return &basev0.Endpoint{
//		Name:        endpoint.Name,
//		Application: endpoint.Application,
//		Service:     endpoint.Service,
//		Visibility:  endpoint.Visibility,
//		Description: endpoint.Description,
//		Api:         CloneAPI(endpoint.Api),
//	}
//
//}

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

func FindRestEndpoint(ctx context.Context, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	for _, e := range endpoints {
		if IsRest(ctx, e) != nil {
			return e, nil
		}
	}
	return nil, fmt.Errorf("no rest endpoint found")
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
