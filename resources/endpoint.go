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
	// VisibilityExternal is a deprecated visibility value. External is a
	// location, not a permission: use Location = LocationExternal alongside a
	// real visibility. Kept so raw protos and legacy YAML keep loading.
	VisibilityExternal Visibility = "external"
	// VisibilityPublic represents an endpoint accessible from outside the workspace.
	VisibilityPublic Visibility = "public"
	// VisibilityModule is a deprecated alias for VisibilityInternal with every
	// module allow-listed. Kept so legacy YAML keeps loading.
	VisibilityModule Visibility = "module"
	// VisibilityInternal represents an endpoint reachable from an explicit
	// allow-list of other modules (see Endpoint.AllowModules).
	VisibilityInternal Visibility = "internal"
	// VisibilityPrivate represents an endpoint that is only accessible within the module
	VisibilityPrivate Visibility = "private"
)

// LocationExternal marks an endpoint that lives outside the system (a managed
// resource resolved by DNS rather than an allocated port).
const LocationExternal = "external"

// AllowAllModules is the wildcard allow-list entry that grants every module
// access to an internal endpoint.
const AllowAllModules = "*"

// Endpoint is the fundamental entity that standardize communication between services.
type Endpoint struct {
	Name        string `yaml:"name"`
	Service     string `yaml:"service,omitempty"`
	Module      string `yaml:"module,omitempty"`
	Description string `yaml:"description,omitempty"`
	Visibility  string `yaml:"visibility,omitempty"`
	API         string `yaml:"api,omitempty"`
	// Location describes where the endpoint lives, independently of visibility.
	Location string `yaml:"location,omitempty"`
	// AllowModules lists the modules permitted to reach an internal endpoint.
	AllowModules []string `yaml:"allow-modules,omitempty"`
}

func validateEndpointNames(endpoints []*Endpoint) error {
	seen := make(map[string]struct{}, len(endpoints))
	for _, endpoint := range endpoints {
		if endpoint == nil {
			return fmt.Errorf("endpoint cannot be nil")
		}
		if _, exists := seen[endpoint.Name]; exists {
			return fmt.Errorf("duplicate endpoint name %q", endpoint.Name)
		}
		seen[endpoint.Name] = struct{}{}
	}
	return nil
}

func (endpoint *Endpoint) postLoad(ctx context.Context) {
	// "module" and "external" are deprecated visibility values. They are
	// interpreted by External() and AllowsModule() rather than rewritten here,
	// so the authored file round-trips unchanged; warn so workspaces migrate to
	// the new axes ("internal" + "allow-modules", and "location: external").
	switch endpoint.Visibility {
	case VisibilityModule:
		wool.Get(ctx).Warn("endpoint visibility 'module' is deprecated; use 'internal' with an explicit 'allow-modules' list",
			wool.NameField(endpoint.Name))
	case VisibilityExternal:
		wool.Get(ctx).Warn("endpoint visibility 'external' is deprecated; use 'location: external' alongside a real visibility",
			wool.NameField(endpoint.Name))
	}
	if endpoint.Visibility == "" {
		endpoint.Visibility = VisibilityPrivate
	}
	if endpoint.API == "" && slices.Contains(standards.APIS(), endpoint.Name) {
		endpoint.API = endpoint.Name
	}
}

// External reports whether the endpoint lives outside the system.
func (endpoint *Endpoint) External() bool {
	return endpoint.Location == LocationExternal || endpoint.Visibility == VisibilityExternal
}

// AllowsModule reports whether a service in the given module may reach this
// endpoint. Access is always granted within the owning module; across modules
// it follows the visibility: public is open, internal consults AllowModules
// (with "*" as a wildcard), the deprecated "module" and "external" values mean
// every module, and everything else is denied.
func (endpoint *Endpoint) AllowsModule(module string) bool {
	if module == endpoint.Module {
		return true
	}
	switch endpoint.Visibility {
	case VisibilityPublic, VisibilityModule, VisibilityExternal:
		return true
	case VisibilityInternal:
		for _, allowed := range endpoint.AllowModules {
			if allowed == AllowAllModules || allowed == module {
				return true
			}
		}
	}
	return false
}

// IsExternalEndpoint reports whether a proto endpoint lives outside the system.
func IsExternalEndpoint(e *basev0.Endpoint) bool {
	return e.Location == LocationExternal || e.Visibility == VisibilityExternal
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
		Name:         endpoint.Name,
		Module:       endpoint.Module,
		Service:      endpoint.Service,
		Api:          endpoint.API,
		Visibility:   endpoint.Visibility,
		Description:  endpoint.Description,
		Location:     endpoint.Location,
		AllowModules: endpoint.AllowModules,
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
		Name:         e.Name,
		Module:       e.Module,
		Service:      e.Service,
		Visibility:   e.Visibility,
		Description:  e.Description,
		API:          e.Api,
		Location:     e.Location,
		AllowModules: e.AllowModules,
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
		Name:         e.Name,
		Visibility:   e.Visibility,
		Description:  e.Description,
		Api:          e.Api,
		ApiDetails:   LightAPI(e.ApiDetails),
		Location:     e.Location,
		AllowModules: e.AllowModules,
	}
}

func IsRest(_ context.Context, endpoint *basev0.Endpoint) *basev0.RestAPI {
	if endpoint == nil {
		return nil
	}
	if endpoint.Api != standards.REST {
		return nil
	}
	if endpoint.ApiDetails == nil {
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
	if endpoint.ApiDetails == nil {
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
	if endpoint.ApiDetails == nil {
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
	if endpoint.ApiDetails == nil {
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
	buf.WriteString(endpoint.Location)
	buf.WriteString(strings.Join(endpoint.AllowModules, ","))
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

func FindEndpointsByAPI(_ context.Context, api string, endpoints []*basev0.Endpoint) []*basev0.Endpoint {
	var matches []*basev0.Endpoint
	for _, endpoint := range endpoints {
		if endpoint != nil && endpoint.Api == api {
			matches = append(matches, endpoint)
		}
	}
	return matches
}

func findTypedEndpointsByAPI(ctx context.Context, api string, endpoints []*basev0.Endpoint) []*basev0.Endpoint {
	matches := FindEndpointsByAPI(ctx, api, endpoints)
	return slices.DeleteFunc(matches, func(endpoint *basev0.Endpoint) bool {
		switch api {
		case standards.GRPC:
			return IsGRPC(ctx, endpoint) == nil
		case standards.REST:
			return IsRest(ctx, endpoint) == nil
		case standards.HTTP:
			return IsHTTP(ctx, endpoint) == nil
		case standards.TCP:
			return IsTCP(ctx, endpoint) == nil
		default:
			return false
		}
	})
}

func findUniqueEndpoint(api string, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	switch len(endpoints) {
	case 0:
		return nil, fmt.Errorf("no %s endpoint found", api)
	case 1:
		return endpoints[0], nil
	default:
		names := make([]string, 0, len(endpoints))
		for _, endpoint := range endpoints {
			names = append(names, endpoint.Name)
		}
		slices.Sort(names)
		return nil, fmt.Errorf("multiple %s endpoints found (%s); specify an endpoint name", api, strings.Join(names, ", "))
	}
}

func findConventionalEndpoint(api string, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	for _, endpoint := range endpoints {
		if endpoint.Name == api {
			return endpoint, nil
		}
	}
	return findUniqueEndpoint(api, endpoints)
}

func FindEndpoint(ctx context.Context, name, api string, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	matches := findTypedEndpointsByAPI(ctx, api, endpoints)
	if name != "" {
		matches = slices.DeleteFunc(matches, func(endpoint *basev0.Endpoint) bool {
			return endpoint.Name != name
		})
	}
	return findUniqueEndpoint(api, matches)
}

func serviceDependencyCandidates(service *ServiceDependency, endpoints []*basev0.Endpoint) ([]*basev0.Endpoint, error) {
	if service == nil {
		return nil, fmt.Errorf("service dependency cannot be nil")
	}
	var candidates []*basev0.Endpoint
	seenNames := make(map[string]struct{})
	for _, endpoint := range endpoints {
		if endpoint == nil {
			return nil, fmt.Errorf("dependency endpoint cannot be nil")
		}
		if endpoint.Service == service.Name && endpoint.Module == service.Module {
			if _, exists := seenNames[endpoint.Name]; exists {
				return nil, fmt.Errorf("service dependency %s received duplicate endpoint name %q", service.Unique(), endpoint.Name)
			}
			seenNames[endpoint.Name] = struct{}{}
			candidates = append(candidates, endpoint)
		}
	}
	return candidates, nil
}

func endpointReferenceKey(reference *EndpointReference) string {
	return reference.Name + "\x00" + reference.API
}

func endpointReferenceDescription(reference *EndpointReference) string {
	switch {
	case reference.Name != "" && reference.API != "":
		return fmt.Sprintf("endpoint %q with API %q", reference.Name, reference.API)
	case reference.Name != "":
		return fmt.Sprintf("endpoint %q", reference.Name)
	default:
		return fmt.Sprintf("API %q", reference.API)
	}
}

func validateEndpointReferences(service *ServiceDependency) error {
	seen := make(map[string]struct{}, len(service.Endpoints))
	for _, reference := range service.Endpoints {
		if reference == nil {
			return fmt.Errorf("service dependency %s has a nil endpoint reference", service.Unique())
		}
		if reference.Name == "" && reference.API == "" {
			return fmt.Errorf("service dependency %s has an empty endpoint reference", service.Unique())
		}
		key := endpointReferenceKey(reference)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("service dependency %s declares %s more than once", service.Unique(), endpointReferenceDescription(reference))
		}
		seen[key] = struct{}{}
	}
	return nil
}

func endpointReferenceMatches(reference *EndpointReference, endpoint *basev0.Endpoint) bool {
	if reference.Name != "" && reference.Name != endpoint.Name {
		return false
	}
	return reference.API == "" || reference.API == endpoint.Api
}

func resolveServiceDependencyEndpoints(service *ServiceDependency, endpoints []*basev0.Endpoint, requireDeclared bool) ([]*basev0.Endpoint, error) {
	candidates, err := serviceDependencyCandidates(service, endpoints)
	if err != nil {
		return nil, err
	}
	if len(service.Endpoints) == 0 {
		return candidates, nil
	}
	if err := validateEndpointReferences(service); err != nil {
		return nil, err
	}

	selectedNames := make(map[string]struct{}, len(service.Endpoints))
	var selected []*basev0.Endpoint
	for _, reference := range service.Endpoints {
		var matches []*basev0.Endpoint
		for _, endpoint := range candidates {
			if endpointReferenceMatches(reference, endpoint) {
				matches = append(matches, endpoint)
			}
		}
		if len(matches) == 0 {
			if requireDeclared {
				return nil, fmt.Errorf("service dependency %s declares undeclared %s", service.Unique(), endpointReferenceDescription(reference))
			}
			continue
		}
		if len(matches) > 1 {
			names := make([]string, 0, len(matches))
			for _, endpoint := range matches {
				names = append(names, endpoint.Name)
			}
			slices.Sort(names)
			return nil, fmt.Errorf("service dependency %s has multiple %s endpoints (%s); specify an endpoint name", service.Unique(), reference.API, strings.Join(names, ", "))
		}
		endpoint := matches[0]
		if _, exists := selectedNames[endpoint.Name]; exists {
			return nil, fmt.Errorf("service dependency %s selects endpoint %q more than once", service.Unique(), endpoint.Name)
		}
		selectedNames[endpoint.Name] = struct{}{}
		selected = append(selected, endpoint)
	}
	return selected, nil
}

func ResolveServiceDependencyEndpoints(service *ServiceDependency, endpoints []*basev0.Endpoint) ([]*basev0.Endpoint, error) {
	return resolveServiceDependencyEndpoints(service, endpoints, true)
}

func SelectServiceDependencyEndpoints(service *ServiceDependency, endpoints []*basev0.Endpoint) ([]*basev0.Endpoint, error) {
	return resolveServiceDependencyEndpoints(service, endpoints, false)
}

func ValidateServiceDependencyEndpoints(dependency *ServiceDependency, endpoints []*basev0.Endpoint) error {
	resolved, err := ResolveServiceDependencyEndpoints(dependency, endpoints)
	if err != nil {
		return err
	}
	if len(dependency.Endpoints) != 0 {
		return nil
	}
	byAPI := make(map[string][]string)
	for _, endpoint := range resolved {
		byAPI[endpoint.Api] = append(byAPI[endpoint.Api], endpoint.Name)
	}
	apis := make([]string, 0, len(byAPI))
	for api := range byAPI {
		apis = append(apis, api)
	}
	slices.Sort(apis)
	for _, api := range apis {
		names := byAPI[api]
		if len(names) < 2 {
			continue
		}
		slices.Sort(names)
		return fmt.Errorf("service dependency %s has multiple %s endpoints (%s); declare endpoint names", dependency.Unique(), api, strings.Join(names, ", "))
	}
	return nil
}

func ValidateDependencyEndpoints(dependencies []*ServiceDependency, endpoints []*basev0.Endpoint) error {
	for _, dependency := range dependencies {
		candidates, err := serviceDependencyCandidates(dependency, endpoints)
		if err != nil {
			return err
		}
		if len(candidates) == 0 {
			continue
		}
		if err := ValidateServiceDependencyEndpoints(dependency, endpoints); err != nil {
			return err
		}
	}
	return nil
}

func findEndpointFromService(ctx context.Context, service *ServiceDependency, api string, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	resolved, err := ResolveServiceDependencyEndpoints(service, endpoints)
	if err != nil {
		return nil, err
	}
	matches := findTypedEndpointsByAPI(ctx, api, resolved)
	if len(matches) == 0 {
		return nil, nil
	}
	return findUniqueEndpoint(api, matches)
}

func FindGRPCEndpoint(ctx context.Context, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	return findConventionalEndpoint(standards.GRPC, findTypedEndpointsByAPI(ctx, standards.GRPC, endpoints))
}

func FindGRPCEndpointFromService(ctx context.Context, service *ServiceDependency, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	return findEndpointFromService(ctx, service, standards.GRPC, endpoints)
}

func FindRestEndpointFromService(ctx context.Context, service *ServiceDependency, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	return findEndpointFromService(ctx, service, standards.REST, endpoints)
}

func FindRestEndpoint(ctx context.Context, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	return findConventionalEndpoint(standards.REST, findTypedEndpointsByAPI(ctx, standards.REST, endpoints))
}

func FindHTTPEndpoint(ctx context.Context, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	return findConventionalEndpoint(standards.HTTP, findTypedEndpointsByAPI(ctx, standards.HTTP, endpoints))
}

func FindConnectEndpoint(ctx context.Context, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	return findConventionalEndpoint(standards.CONNECT, FindEndpointsByAPI(ctx, standards.CONNECT, endpoints))
}

func FindTCPEndpoint(ctx context.Context, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	return findConventionalEndpoint(standards.TCP, findTypedEndpointsByAPI(ctx, standards.TCP, endpoints))
}

func FindTCPEndpointWithName(ctx context.Context, name string, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	return FindEndpoint(ctx, name, standards.TCP, endpoints)
}

func HasPublicEndpoints(endpoints []*basev0.Endpoint) bool {
	for _, endpoint := range endpoints {
		if endpoint.Visibility == VisibilityPublic {
			return true
		}
	}
	return false
}
