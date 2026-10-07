package resources

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/standards"
	"github.com/codefly-dev/core/wool"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

type Visibility = string

// Visibility is REACH, and nothing else: who may call the endpoint. It has no
// addressing consequence — whether an address reachable from outside the
// workspace is allocated is Exposure, declared beside it — and it names
// nobody: which modules actually reach an endpoint is derived from the
// consumers' declared service dependencies (DeriveAllowModules), never
// written on the endpoint.
const (
	// VisibilityPrivate is reachable from the same module only.
	VisibilityPrivate Visibility = "private"
	// VisibilityInternal is reachable across modules, from within the
	// workspace: any module of the composition may declare a dependency on
	// it, and nothing outside the composition may reach it. It is the whole
	// of what a host can honestly say about itself — "reachable by whatever
	// composes me" — without naming its composers.
	VisibilityInternal Visibility = "internal"
	// VisibilityPublic is reachable from outside the workspace. It says
	// nothing about an address: a public endpoint with no exposure is
	// reachable by anything that can address it and published nowhere.
	VisibilityPublic Visibility = "public"
)

// LocationExternal marks an endpoint that lives outside the system (a managed
// resource resolved by DNS rather than an allocated port).
const LocationExternal = "external"

// ExposureNone states that no address reachable from outside the workspace is
// allocated. A public endpoint states its exposure — "public" or "none" —
// rather than omitting it, so a manifest written when `visibility: public`
// meant an outward address fails to load instead of quietly losing it.
const ExposureNone = "none"

// ExposurePublic marks an endpoint that is allocated an address reachable from
// outside the workspace — the Public network instance a run generates beside
// the Native and Container ones, and whatever a deployment renders from it (a
// load balancer, a route). Exposure is ADDRESSING, the second job the word
// "public" used to do: it is declared on its own, beside the visibility, so an
// endpoint reachable by every module but addressable from nowhere outside is
// `visibility: public` with no exposure, and one that must be addressed from
// outside says so. Internet exposure — a route with hostnames — is still the
// deployment's ingress, declared there; exposure is what gives it an endpoint
// to route to.
const ExposurePublic = "public"

// KnownVisibility reports whether a visibility value is one the model defines.
// It is the ONE list of them: ValidateEndpointDeclaration judges every
// declaration against it — at load, at selection, at dependency wiring — so a
// value the model does not know is refused as an invalid declaration everywhere
// rather than read as "private" by one path and "denied" by another;
// AllowsModule, which only ever runs on a declaration already judged, reads
// the permission of a known value. The former spellings "module" and
// "external" are not known: "module" was reach written as a permission to
// every module, now written as "internal" (which names nobody), and "external"
// was a location written as a permission, now written as "location: external"
// beside the visibility that applies.
func KnownVisibility(visibility Visibility) bool {
	switch visibility {
	case "", VisibilityPrivate, VisibilityInternal, VisibilityPublic:
		return true
	}
	return false
}

// KnownLocation reports whether a location value is one the model defines.
func KnownLocation(location string) bool {
	return location == "" || location == LocationExternal
}

// KnownExposure reports whether an exposure value is one the model defines.
func KnownExposure(exposure string) bool {
	return exposure == "" || exposure == ExposureNone || exposure == ExposurePublic
}

// Endpoint is the fundamental entity that standardize communication between services.
type Endpoint struct {
	Name        string `yaml:"name"`
	Service     string `yaml:"service,omitempty"`
	Module      string `yaml:"module,omitempty"`
	Description string `yaml:"description,omitempty"`
	// Visibility is who may reach the endpoint: private, internal or public.
	// It is reach only; see Exposure for addressing.
	Visibility string `yaml:"visibility,omitempty"`
	API        string `yaml:"api,omitempty"`
	Secured    bool   `yaml:"secured,omitempty"`
	// Location describes where the endpoint lives, independently of visibility.
	Location string `yaml:"location,omitempty"`
	// Exposure describes whether the endpoint is allocated an address reachable
	// from outside the workspace, independently of visibility.
	Exposure string `yaml:"exposure,omitempty"`
	// AllowModules is never a grant. The allow-list of an endpoint is DERIVED
	// from the declared service dependencies of the composition's consumers
	// (Workspace.DeriveAllowModules): a module asks for what it consumes, in
	// its own declaration, and the composition joins the asks. A list written
	// on the endpoint would be the target naming its own consumers, which no
	// module may do, so the YAML key is refused by presence before decoding
	// (any spelling, any value, null and [] included), the wire field by number
	// before projection, and a value set here in memory by
	// ValidateEndpointDeclaration. Nothing reads it; nothing writes it.
	AllowModules []string `yaml:"-"`
	// Health declares what "healthy" means for this endpoint. Absence keeps the
	// legacy transport-only semantics.
	Health *Health `yaml:"health,omitempty"`

	// authored retains what the YAML declared when a module's interface decides
	// what the endpoint exports instead, so a save writes the author's value
	// back rather than the exported one.
	authored *authoredExport
}

// UnmarshalYAML decodes an endpoint mapping after judging its keys: an unknown
// key and the forbidden allow-modules key, in any spelling, are refused by
// presence, BEFORE decoding loses them — a non-strict decoder would drop the
// one and read null as absence for the other.
func (endpoint *Endpoint) UnmarshalYAML(node *yaml.Node) error {
	return endpoint.decodeYAML(node, "")
}

// decodeYAML is the one endpoint decoder, with one decoder rule deleted; "" is
// every real path, and the kit's self-check is its only caller with a name.
func (endpoint *Endpoint) decodeYAML(node *yaml.Node, deleted string) error {
	keys, name := mappingKeys(node)
	if err := validateEndpointKeys(name, keys, deleted); err != nil {
		return err
	}
	type plain Endpoint
	var decoded plain
	if err := node.Decode(&decoded); err != nil {
		return err
	}
	*endpoint = Endpoint(decoded)
	return nil
}

// authoredExport is the export-relevant part of an endpoint as its service
// wrote it, kept aside while the module's interface entry is what the loaded
// endpoint carries.
type authoredExport struct {
	visibility string
	exposure   string
}

// exportAs sets the visibility the endpoint carries across module boundaries,
// keeping the authored values for save. The interface entry is the whole export
// declaration: once the module has spoken, the service's own visibility is not
// what a consumer is judged against. An endpoint the module exports public
// states its exposure like any public endpoint: the service that declared none
// (it was not public to the service) is exported with none, since the module
// grants reach and never an address.
func (endpoint *Endpoint) exportAs(visibility Visibility) {
	if endpoint.authored == nil {
		endpoint.authored = &authoredExport{visibility: endpoint.Visibility, exposure: endpoint.Exposure}
	}
	endpoint.Visibility = visibility
	if visibility == VisibilityPublic && endpoint.Exposure == "" {
		endpoint.Exposure = ExposureNone
	}
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

// postLoad completes a declaration the loader read and refuses one the model
// cannot judge, with every rule of ValidateEndpointDeclaration, here at the
// source, so that no later path has to decide what "application" means or
// whether an authored allow-list grants anything.
func (endpoint *Endpoint) postLoad() error {
	return endpoint.postLoadWithout("")
}

// postLoadWithout is postLoad with one declaration rule deleted; "" deletes
// none. It exists for the kit's self-check, which proves every rule is one a
// shipped fixture would miss.
func (endpoint *Endpoint) postLoadWithout(deleted string) error {
	if err := validateEndpointDeclaration(endpoint.Declaration(), deleted); err != nil {
		return err
	}
	if endpoint.Visibility == "" {
		endpoint.Visibility = VisibilityPrivate
	}
	if endpoint.API == "" && slices.Contains(standards.APIS(), endpoint.Name) {
		endpoint.API = endpoint.Name
	}
	return nil
}

// Declaration is the export-relevant part of this endpoint, as
// ValidateEndpointDeclaration judges it.
func (endpoint *Endpoint) Declaration() EndpointDeclaration {
	return EndpointDeclaration{
		Service:      endpoint.Service,
		Name:         endpoint.Name,
		Visibility:   endpoint.Visibility,
		Location:     endpoint.Location,
		Exposure:     endpoint.Exposure,
		AllowModules: endpoint.AllowModules,
	}
}

// EndpointDeclarationOf is the export-relevant part of a proto endpoint, as
// ValidateEndpointDeclaration judges it. The wire model defines no allow-list
// — a grant is not something an endpoint can say about itself — and bytes for
// a field the schema does not define, the reserved allow_modules among them,
// are carried here as unknown field numbers for the rule to refuse, never
// dropped by the projection.
func EndpointDeclarationOf(e *basev0.Endpoint) EndpointDeclaration {
	declaration := EndpointDeclaration{
		Service:    e.GetService(),
		Name:       e.GetName(),
		Visibility: e.GetVisibility(),
		Location:   e.GetLocation(),
		Exposure:   e.GetExposure(),
	}
	if e != nil {
		declaration.UnknownWireFields = UnknownWireFields(e.ProtoReflect().GetUnknown())
	}
	return declaration
}

// External reports whether the endpoint lives outside the system. Location is
// the one record of it: where an endpoint lives and who may reach it are two
// axes, and a visibility never says where.
func (endpoint *Endpoint) External() bool {
	return endpoint.Location == LocationExternal
}

// Exposed reports whether the endpoint is allocated an address reachable from
// outside the workspace. Exposure is the one record of it: a visibility never
// says whether an address exists, only who may call.
func (endpoint *Endpoint) Exposed() bool {
	return endpoint.Exposure == ExposurePublic
}

// AllowsModule reports whether a service in the given module may reach this
// endpoint. Access is always granted within the owning module; across modules
// it follows the visibility: public and internal are reachable from any
// module of the composition — internal stops at the workspace boundary, which
// no module is on the far side of — and private is denied. Which modules DO
// reach an endpoint is the derived allow-list (DeriveAllowModules), a record
// of declared asks, never an input to this answer.
func (endpoint *Endpoint) AllowsModule(module string) bool {
	if module == endpoint.Module {
		return true
	}
	switch endpoint.Visibility {
	case VisibilityPublic, VisibilityInternal:
		return true
	}
	return false
}

// EndpointSecured reports whether the endpoint expects TLS. Each protocol
// records it in its own API details, so a caller that needs to dial the
// endpoint must read it from here rather than assume plaintext.
func EndpointSecured(e *basev0.Endpoint) bool {
	switch details := e.GetApiDetails().GetValue().(type) {
	case *basev0.API_Grpc:
		return details.Grpc.GetSecured()
	case *basev0.API_Http:
		return details.Http.GetSecured()
	case *basev0.API_Rest:
		return details.Rest.GetSecured()
	default:
		return false
	}
}

// IsExternalEndpoint reports whether a proto endpoint lives outside the system.
func IsExternalEndpoint(e *basev0.Endpoint) bool {
	return e.GetLocation() == LocationExternal
}

// IsExposedEndpoint reports whether a proto endpoint is allocated an address
// reachable from outside the workspace. It is the condition the runtime
// manager emits a Public network instance on, and the one a deployment renders
// an outward address from; a visibility is never that condition.
func IsExposedEndpoint(e *basev0.Endpoint) bool {
	return e.GetExposure() == ExposurePublic
}

func (endpoint *Endpoint) preSave() {
	if endpoint.authored != nil {
		endpoint.Visibility = endpoint.authored.visibility
		endpoint.Exposure = endpoint.authored.exposure
	}
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
	if err := ValidateEndpointDeclaration(endpoint.Declaration()); err != nil {
		return nil, err
	}
	if endpoint.API == "" && standards.IsSupportedAPI(endpoint.Name) == nil {
		endpoint.API = endpoint.Name
	}
	if err := standards.IsSupportedAPI(endpoint.API); err != nil {
		return nil, fmt.Errorf("unsupported api: %s", endpoint.API)
	}
	if endpoint.Secured && endpoint.API == standards.TCP {
		return nil, fmt.Errorf("endpoint %q cannot declare TLS for the untyped tcp API", endpoint.Name)
	}
	health, err := endpoint.Health.Proto(endpoint.API)
	if err != nil {
		return nil, fmt.Errorf("endpoint %q health: %w", endpoint.Name, err)
	}
	e := &basev0.Endpoint{
		Name:        endpoint.Name,
		Module:      endpoint.Module,
		Service:     endpoint.Service,
		Api:         endpoint.API,
		Visibility:  endpoint.Visibility,
		Description: endpoint.Description,
		Location:    endpoint.Location,
		Exposure:    endpoint.Exposure,
		Health:      health,
	}
	switch endpoint.API {
	case standards.REST:
		e.ApiDetails = ToRestAPI(&basev0.RestAPI{Secured: endpoint.Secured})
	case standards.GRPC:
		e.ApiDetails = ToGrpcAPI(&basev0.GrpcAPI{Secured: endpoint.Secured})
	case standards.HTTP, standards.CONNECT, standards.MCP:
		e.ApiDetails = ToHTTPAPI(&basev0.HttpAPI{Secured: endpoint.Secured})
	case standards.TCP:
		e.ApiDetails = ToTCPAPI(&basev0.TcpAPI{})
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
		Secured:     EndpointSecured(e),
		Location:    e.Location,
		Exposure:    e.Exposure,
		Health:      HealthFromProto(e.Health),
	}
}

// FromProtoEndpoints projects proto endpoints into the resource model, judging
// each declaration whole first — the wire's unknown fields included — so a
// proto that passed per-field schema validation is still refused here when
// its declaration contradicts itself or carries a reserved field.
func FromProtoEndpoints(es ...*basev0.Endpoint) ([]*Endpoint, error) {
	var endpoints []*Endpoint
	for _, e := range es {
		if err := ValidateEndpointDeclaration(EndpointDeclarationOf(e)); err != nil {
			return nil, err
		}
		if err := ValidateEndpointHealth(e); err != nil {
			return nil, err
		}
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
		Location:    e.Location,
		Exposure:    e.Exposure,
		Health:      e.Health,
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
	buf.WriteString(endpoint.Exposure)
	buf.WriteString(endpoint.Api)
	buf.WriteString(endpoint.ApiDetails.String())
	if endpoint.Health != nil {
		encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(endpoint.Health)
		if err != nil {
			return "", err
		}
		buf.Write(encoded)
	}
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
	if service.interfaceOnly() {
		return nil, fmt.Errorf("service dependency on interface %s is not bound to a provider", service.Interface)
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
	// An edge that consumes no endpoint — a completion prerequisite, an
	// external capability — selects none: stage participation is not
	// consumption, and an omitted list on such an edge is not "all".
	if !service.Kind.ConsumesEndpoints() {
		return nil, nil
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

// dependencyEndpointVerdicts judges the edge by the provenance of its two ends
// (JudgeCompositionEdge), then selects the endpoints the dependency consumes
// and judges each against the consuming module, returning the permitted ones
// and the reasons the rest were refused. It is the one body behind every "may
// this consumer depend on this endpoint" answer in the codebase, and the
// provenance is in its signature so that no reader can ask without it.
func dependencyEndpointVerdicts(provenance Provenance, consumerModule string, dependency *ServiceDependency, endpoints []*basev0.Endpoint) ([]*basev0.Endpoint, []error, error) {
	if dependency == nil {
		return nil, nil, fmt.Errorf("service dependency cannot be nil")
	}
	if !dependency.interfaceOnly() {
		producerModule := dependency.Module
		if producerModule == "" {
			producerModule = consumerModule
		}
		if err := JudgeCompositionEdge(provenance, consumerModule, producerModule, dependency); err != nil {
			return nil, nil, err
		}
	}
	resolved, err := SelectServiceDependencyEndpoints(dependency, endpoints)
	if err != nil {
		return nil, nil, err
	}
	var permitted []*basev0.Endpoint
	var denials []error
	for _, endpoint := range resolved {
		if err := ValidateEndpointVisibility(consumerModule, endpoint.Module, EndpointDeclarationOf(endpoint)); err != nil {
			// A declaration the model cannot judge is a fault of the producer's
			// manifest, not a verdict on this consumer: filing it as a denial
			// would let "consumes all" drop the endpoint with nothing said —
			// for the owning module too, which no visibility ever denies.
			if errors.Is(err, ErrInvalidEndpointDeclaration) {
				return nil, nil, err
			}
			denials = append(denials, err)
			continue
		}
		permitted = append(permitted, endpoint)
	}
	return permitted, denials, nil
}

// PermittedDependencyEndpoints returns what a consumer ends up wired to: the
// endpoints of the dependency its module may receive, with the rest dropped. It
// passes no judgement on the edge itself — ConsumedDependencyEndpoints is the
// same selection with the verdict attached — so a description of a composition
// that will not run still says what it would have carried.
func PermittedDependencyEndpoints(provenance Provenance, consumerModule string, dependency *ServiceDependency, endpoints []*basev0.Endpoint) ([]*basev0.Endpoint, error) {
	permitted, _, err := dependencyEndpointVerdicts(provenance, consumerModule, dependency, endpoints)
	return permitted, err
}

// ConsumedDependencyEndpoints returns the producer endpoints a dependency
// consumes, refusing the edge when the consuming module may not have them. It
// is the single answer to "may this consumer depend on this endpoint": the
// static passes and every path that hands a consumer an address resolve through
// it, with the composition's provenance in the signature, so a composition
// validation refuses can never be one a run resolves — a solution's route to
// a module included. There is no reader that holds no composition: a holder
// of addresses that can find none refuses them as unjudged
// (ErrUnjudgedProvenance) rather than wiring them.
//
// A dependency that names endpoints consumes exactly those, and naming one the
// producer does not grant is an error naming that endpoint: the consumer's
// author asked for it, so dropping it would leave their SDK missing a declared
// accessor with nothing to explain why. A dependency that names none consumes
// "all", which can only mean all it is permitted — otherwise a producer adding
// one private endpoint breaks every consumer that did not enumerate, and
// changes their SDK surface from the outside. Being permitted none of them is
// still a violation: the edge is declared and the producer's export boundary
// grants nothing for it, so resolving it to an empty set would leave the
// consumer silently unwired.
func ConsumedDependencyEndpoints(provenance Provenance, consumerModule string, dependency *ServiceDependency, endpoints []*basev0.Endpoint) ([]*basev0.Endpoint, error) {
	permitted, denials, err := dependencyEndpointVerdicts(provenance, consumerModule, dependency, endpoints)
	if err != nil {
		return nil, err
	}
	if len(denials) == 0 {
		return permitted, nil
	}
	if len(dependency.Endpoints) != 0 {
		return nil, denials[0]
	}
	if len(permitted) > 0 {
		return permitted, nil
	}
	reasons := make([]string, 0, len(denials))
	for _, denial := range denials {
		reasons = append(reasons, denial.Error())
	}
	return nil, fmt.Errorf("service dependency %s names no endpoint and none of the endpoints it would consume permit module %q: %s",
		dependency.Unique(), consumerModule, strings.Join(reasons, "; "))
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

func FindMCPEndpoint(ctx context.Context, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	return findConventionalEndpoint(standards.MCP, FindEndpointsByAPI(ctx, standards.MCP, endpoints))
}

func FindTCPEndpoint(ctx context.Context, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	return findConventionalEndpoint(standards.TCP, findTypedEndpointsByAPI(ctx, standards.TCP, endpoints))
}

func FindTCPEndpointWithName(ctx context.Context, name string, endpoints []*basev0.Endpoint) (*basev0.Endpoint, error) {
	return FindEndpoint(ctx, name, standards.TCP, endpoints)
}

// HasExposedEndpoints reports whether any endpoint is allocated an address
// reachable from outside the workspace. It answers an ADDRESSING question — does
// a deployment have something to route to — never who may call.
func HasExposedEndpoints(endpoints []*basev0.Endpoint) bool {
	for _, endpoint := range endpoints {
		if IsExposedEndpoint(endpoint) {
			return true
		}
	}
	return false
}
