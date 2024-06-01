package resources

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/wool"
	multierror "github.com/hashicorp/go-multierror"

	"github.com/codefly-dev/core/shared"
	"gopkg.in/yaml.v3"
)

type HTTPMethod string

const (
	HTTPMethodGet     HTTPMethod = "GET"
	HTTPMethodPut     HTTPMethod = "PUT"
	HTTPMethodPost    HTTPMethod = "POST"
	HTTPMethodDelete  HTTPMethod = "DELETE"
	HTTPMethodPatch   HTTPMethod = "PATCH"
	HTTPMethodOptions HTTPMethod = "OPTIONS"
	HTTPMethodHead    HTTPMethod = "HEAD"
)

// RestRouteGroup holds all the routes corresponding to the SAME path
// Each Route corresponds to a DIFFERENT HTTP method
// HTTP methods correspond to individual routes
type RestRouteGroup struct {
	// HTTP Path
	Path string `yaml:"path"`
	// Routes for each HTTP Method
	Routes  []*RestRoute `yaml:"routes"`
	Module  string       `yaml:"-"`
	Service string       `yaml:"-"`
}

type RestRoute struct {
	Path   string
	Method HTTPMethod
}

func (route *RestRoute) Proto() (*basev0.RestRoute, error) {
	proto := &basev0.RestRoute{
		Path:   route.Path,
		Method: ConvertHTTPMethodToProto(route.Method),
	}
	err := Validate(proto)
	if err != nil {
		return nil, err
	}
	return proto, nil
}

type ExtendedRestRoute[T any] struct {
	RestRoute `yaml:",inline"`

	Extension T `yaml:"extension"`
}

func (g *RestRouteGroup) ServiceUnique() string {
	return ServiceUnique(g.Module, g.Service)
}

type ExtendedRestRouteGroup[T any] struct {
	Path    string                  `yaml:"path"`
	Routes  []*ExtendedRestRoute[T] `yaml:"routes"`
	Module  string                  `yaml:"-"`
	Service string                  `yaml:"-"`
}

func (g *ExtendedRestRouteGroup[T]) ServiceUnique() string {
	return ServiceUnique(g.Module, g.Service)
}

func (g *ExtendedRestRouteGroup[T]) Add(route ExtendedRestRoute[T]) {
	var routes []*ExtendedRestRoute[T]
	var update bool
	for _, r := range g.Routes {
		if r.Path == route.Path && r.Method == route.Method {
			routes = append(routes, &route)
			update = true
		} else {
			routes = append(routes, r)
		}
	}
	if !update {
		routes = append(routes, &route)
	}
	g.Routes = routes
}

func NewExtendedRestRoute[T any](rest RestRoute, value T) *ExtendedRestRoute[T] {
	return &ExtendedRestRoute[T]{
		RestRoute: rest,
		Extension: value,
	}
}

func UnwrapRestRoute[T any](route *ExtendedRestRoute[T]) *RestRoute {
	return &route.RestRoute
}

func UnwrapRestRouteGroup[T any](group *ExtendedRestRouteGroup[T]) *RestRouteGroup {
	var rs []*RestRoute
	for _, r := range group.Routes {
		rs = append(rs, &r.RestRoute)
	}
	return &RestRouteGroup{
		Path:    group.Path,
		Module:  group.Module,
		Service: group.Service,
		Routes:  rs,
	}
}

// RestRouteLoader will return all rest route groups in a directory
type RestRouteLoader struct {
	dir    string
	groups map[string][]*RestRouteGroup
}

func NewRestRouteLoader(ctx context.Context, dir string) (*RestRouteLoader, error) {
	w := wool.Get(ctx).In("NewRestRouteLoader")
	exists, err := shared.DirectoryExists(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot check directory")
	}
	if !exists {
		return nil, w.NewError("directory <%s> does not exist", dir)
	}
	return &RestRouteLoader{dir: dir}, nil
}

const RestRouteFileSuffix = ".rest.codefly.yaml"

func (loader *RestRouteLoader) Load(ctx context.Context) error {
	w := wool.Get(ctx).In("RestRouteLoader::Load")
	groups := make(map[string][]*RestRouteGroup)
	err := filepath.Walk(loader.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return w.Wrapf(err, "error walking path <%s>", path)
		}
		if info.IsDir() {
			return nil
		}
		unique, err := filepath.Rel(loader.dir, filepath.Dir(path))
		if err != nil {
			return err
		}
		ref, err := ParseServiceWithOptionalModule(unique)
		if err != nil {
			return nil
		}
		base := filepath.Base(path)
		var routePath string
		var ok bool
		if routePath, ok = strings.CutSuffix(base, RestRouteFileSuffix); !ok {
			return nil
		}
		routePath = fmt.Sprintf("/%s", routePath)
		group, err := LoadRestRouteGroup(ctx, path)
		if err != nil {
			return err
		}
		// Validate all paths in routes starts with the generic path!
		for _, route := range group.Routes {
			if !strings.HasPrefix(route.Path, group.Path) {
				return w.NewError("route <%s> does not start with path <%s>", route.Path, group.Path)
			}
		}
		group.Path = routePath
		group.Module = ref.Module
		group.Service = ref.Name
		groups[unique] = append(groups[unique], group)
		return nil
	})
	if err != nil {
		return err
	}
	loader.groups = groups
	return nil
}

func (loader *RestRouteLoader) All() []*RestRoute {
	var routes []*RestRoute
	for _, group := range loader.groups {
		for _, route := range group {
			routes = append(routes, route.Routes...)
		}
	}
	return routes
}

func (loader *RestRouteLoader) GroupsFor(unique string) []*RestRouteGroup {
	return loader.groups[unique]
}

func (loader *RestRouteLoader) Groups() []*RestRouteGroup {
	var groups []*RestRouteGroup
	for _, group := range loader.groups {
		groups = append(groups, group...)
	}
	return groups
}

func (loader *RestRouteLoader) GroupFor(unique string, routePath string) *RestRouteGroup {
	for _, g := range loader.groups[unique] {
		if g.Path == routePath {
			return g
		}
	}
	return nil
}

// ExtendedRouteLoader will return all rest route groups in a directory
type ExtendedRouteLoader[T any] struct {
	dir    string
	groups map[string][]*ExtendedRestRouteGroup[T]
}

func NewExtendedRestRouteLoader[T any](ctx context.Context, dir string) (*ExtendedRouteLoader[T], error) {
	w := wool.Get(ctx).In("NewRestRouteLoader")
	exists, err := shared.DirectoryExists(ctx, dir)
	if err != nil {
		return nil, w.Wrapf(err, "cannot check directory")
	}
	if !exists {
		return nil, w.NewError("directory <%s> does not exist", dir)
	}
	return &ExtendedRouteLoader[T]{dir: dir}, nil
}

func (loader *ExtendedRouteLoader[T]) Load(ctx context.Context) error {
	w := wool.Get(ctx).In("RestRouteLoader::Load")
	groups := make(map[string][]*ExtendedRestRouteGroup[T])
	err := filepath.Walk(loader.dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return w.Wrapf(err, "error walking path <%s>", path)
		}
		if info.IsDir() {
			return nil
		}
		unique, err := filepath.Rel(loader.dir, filepath.Dir(path))
		if err != nil {
			return err
		}
		ref, err := ParseServiceWithOptionalModule(unique)
		if err != nil {
			return nil
		}
		base := filepath.Base(path)
		var routePath string
		var ok bool
		if routePath, ok = strings.CutSuffix(base, RestRouteFileSuffix); !ok {
			return nil
		}
		routePath = fmt.Sprintf("/%s", routePath)
		group, err := LoadExtendedRestRouteGroup[T](ctx, path)
		if err != nil {
			return err
		}
		// Validate all paths in routes starts with the generic path!
		for _, route := range group.Routes {
			if !strings.HasPrefix(route.Path, group.Path) {
				return w.NewError("route <%s> does not start with path <%s>", route.Path, group.Path)
			}
		}
		group.Path = routePath
		group.Module = ref.Module
		group.Service = ref.Name
		groups[unique] = append(groups[unique], group)
		return nil
	})
	if err != nil {
		return err
	}
	loader.groups = groups
	return nil
}

func (loader *ExtendedRouteLoader[T]) All() []*ExtendedRestRoute[T] {
	var routes []*ExtendedRestRoute[T]
	for _, group := range loader.groups {
		for _, route := range group {
			routes = append(routes, route.Routes...)
		}
	}
	return routes
}

func (loader *ExtendedRouteLoader[T]) Groups() []*ExtendedRestRouteGroup[T] {
	var groups []*ExtendedRestRouteGroup[T]
	for _, group := range loader.groups {
		groups = append(groups, group...)
	}
	return groups
}

func (loader *ExtendedRouteLoader[T]) GroupsFor(unique string) []*ExtendedRestRouteGroup[T] {
	return loader.groups[unique]
}

func (loader *ExtendedRouteLoader[T]) GroupFor(unique string, routePath string) *ExtendedRestRouteGroup[T] {
	for _, g := range loader.groups[unique] {
		if g.Path == routePath {
			return g
		}
	}
	return nil
}

func (loader *ExtendedRouteLoader[T]) AddGroup(group *ExtendedRestRouteGroup[T]) {
	loader.groups[group.ServiceUnique()] = append(loader.groups[group.ServiceUnique()], group)

}

func (loader *ExtendedRouteLoader[T]) Save(ctx context.Context) error {
	w := wool.Get(ctx).In("RestRouteLoader::Save")
	groups := loader.Groups()
	w.Debug("saving groups", wool.SliceCountField(groups))
	var result error
	for _, group := range groups {
		err := group.Save(ctx, loader.dir)
		if err != nil {
			result = multierror.Append(result, err)
		}
	}
	return result
}

func sanitizeRoute(route string) string {
	route = strings.TrimPrefix(route, "/")
	return strings.ReplaceAll(route, "/", "_")
}

func sanitizePath(route string) string {
	route = strings.TrimPrefix(route, "/")
	return strings.ReplaceAll(route, "/", "__")
}

func FilePathForRest(ctx context.Context, dir string, unique string, routePath string) (string, error) {
	dir = path.Join(dir, unique)
	_, err := shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return "", err
	}
	file := path.Join(dir, fmt.Sprintf("%s%s", sanitizeRoute(routePath), RestRouteFileSuffix))
	return file, nil
}

func (g *RestRouteGroup) Save(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("RestRoute::Save")
	file, err := FilePathForRest(ctx, dir, g.ServiceUnique(), g.Path)
	if err != nil {
		return w.Wrapf(err, "cannot get file path for route to save")
	}
	w.Trace("saving", wool.FileField(file))
	f, err := os.Create(file)
	if err != nil {
		return w.Wrapf(err, "cannot create file for route")
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			w.Error("cannot close file", wool.ErrField(err))
		}
	}(f)
	out, err := yaml.Marshal(g)
	if err != nil {
		return w.Wrapf(err, "cannot marshal route")
	}
	_, err = f.Write(out)
	if err != nil {
		return w.With(wool.FileField(file)).Wrapf(err, "cannot write route")
	}
	return nil
}

func (g *ExtendedRestRouteGroup[T]) Save(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("ExtendedRestRoute::Save")
	file, err := FilePathForRest(ctx, dir, g.ServiceUnique(), g.Path)
	if err != nil {
		return w.Wrapf(err, "cannot get file path for route to save")
	}
	w.Debug("saving", wool.FileField(file), wool.Field("content", g))
	f, err := os.Create(file)
	if err != nil {
		return w.Wrapf(err, "cannot create file for route")
	}
	defer func(f *os.File) {
		err := f.Close()
		if err != nil {
			w.Error("cannot close file", wool.ErrField(err))
		}
	}(f)
	out, err := yaml.Marshal(g)
	if err != nil {
		return w.Wrapf(err, "cannot marshal route")
	}
	_, err = f.Write(out)
	if err != nil {
		return w.With(wool.FileField(file)).Wrapf(err, "cannot write route")
	}
	return nil
}

// Delete a route
func (g *RestRouteGroup) Delete(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("RestRoute::Delete")
	file, err := FilePathForRest(ctx, dir, g.ServiceUnique(), sanitizePath(g.Path))
	if err != nil {
		return w.Wrapf(err, "cannot get file path for route to delete")
	}
	err = os.Remove(file)
	if err != nil {
		return w.Wrapf(err, "cannot delete route file")
	}
	return nil
}

func LoadRestRouteGroup(ctx context.Context, p string) (*RestRouteGroup, error) {
	var err error
	p, err = shared.SolvePath(p)
	if err != nil {
		return nil, err
	}
	r, err := LoadFromPath[RestRouteGroup](ctx, p)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func LoadExtendedRestRouteGroup[T any](ctx context.Context, p string) (*ExtendedRestRouteGroup[T], error) {
	var err error
	p, err = shared.SolvePath(p)
	if err != nil {
		return nil, err
	}
	r, err := LoadFromPath[ExtendedRestRouteGroup[T]](ctx, p)
	if err != nil {
		return nil, err
	}
	return r, nil
}

func AsRestRouteEnvironmentVariable(ctx context.Context, endpoint *basev0.Endpoint) []*EnvironmentVariable {
	w := wool.Get(ctx).In("AsRestRouteEnvironmentVariable")
	var envs []*EnvironmentVariable
	if rest := IsRest(context.Background(), endpoint); rest != nil {
		for _, group := range rest.Groups {
			for _, route := range group.Routes {
				w.Debug("adding", wool.Field("route", route))
				envs = append(envs, RestRoutesAsEnvironmentVariable(endpoint, route))
			}
		}
	}
	return envs

}

func ConvertHTTPMethodToProto(m HTTPMethod) basev0.HTTPMethod {
	switch m {
	case HTTPMethodGet:
		return basev0.HTTPMethod_GET
	case HTTPMethodPost:
		return basev0.HTTPMethod_POST
	case HTTPMethodPut:
		return basev0.HTTPMethod_PUT
	case HTTPMethodDelete:
		return basev0.HTTPMethod_DELETE
	case HTTPMethodPatch:
		return basev0.HTTPMethod_PATCH
	case HTTPMethodOptions:
		return basev0.HTTPMethod_OPTIONS
	case HTTPMethodHead:
		return basev0.HTTPMethod_HEAD
	}
	panic(fmt.Sprintf("unknown HTTP method: <%v>", m))
}

func ConvertHTTPMethodFromProto(m basev0.HTTPMethod) HTTPMethod {
	switch m {
	case basev0.HTTPMethod_GET:
		return HTTPMethodGet
	case basev0.HTTPMethod_POST:
		return HTTPMethodPost
	case basev0.HTTPMethod_PUT:
		return HTTPMethodPut
	case basev0.HTTPMethod_DELETE:
		return HTTPMethodDelete
	case basev0.HTTPMethod_PATCH:
		return HTTPMethodPatch
	case basev0.HTTPMethod_OPTIONS:
		return HTTPMethodOptions
	case basev0.HTTPMethod_HEAD:
		return HTTPMethodHead
	}
	panic(fmt.Sprintf("unknown HTTP method: <%v>", m))
}

func RestRouteFromProto(r *basev0.RestRoute) *RestRoute {
	return &RestRoute{
		Path:   r.Path,
		Method: ConvertHTTPMethodFromProto(r.Method),
	}
}

func GRPCRouteFromProto(e *basev0.Endpoint, grpc *basev0.GrpcAPI, rpc *basev0.RPC) *GRPCRoute {
	return &GRPCRoute{
		Name:        rpc.Name,
		Package:     grpc.Package,
		ServiceName: rpc.ServiceName,
		Service:     e.Service,
		Module:      e.Module,
	}
}

type RouteUnique struct {
	service string
	module  string
	path    string
	method  HTTPMethod
}

func (r RouteUnique) String() string {
	return fmt.Sprintf("%s/%s%s[%s]", r.module, r.service, r.path, r.method)
}

func GroupKey(endpoint *basev0.Endpoint, group *basev0.RestRouteGroup) string {
	return fmt.Sprintf("%s_%s_%s_%s", endpoint.Module, endpoint.Service, endpoint.Name, group.Path)
}

func DetectNewRoutesFromEndpoints(ctx context.Context, endpoints []*basev0.Endpoint, known []*RestRouteGroup) []*RestRouteGroup {
	w := wool.Get(ctx).In("DetectNewRoutes")
	w.Debug("processing endpoints", wool.SliceCountField(endpoints))
	knownRoutes := make(map[string]bool)
	for _, k := range known {
		for _, r := range k.Routes {
			u := RouteUnique{
				service: k.Service,
				module:  k.Module,
				path:    r.Path,
				method:  r.Method,
			}
			knownRoutes[u.String()] = true
		}
	}
	w.Debug("known routes", wool.Field("all", knownRoutes))
	newGroups := make(map[string]*RestRouteGroup)

	for _, e := range endpoints {
		rest := IsRest(ctx, e)
		if rest == nil {
			w.Debug("endpoint not REST", wool.Field("name", e.Name), wool.Field("api", e.Api))
			continue
		}
		w.Debug("found a REST endpoint", wool.Field("name", e.Name), wool.Field("api", e.Api), wool.SliceCountField(rest.Groups))
		for _, group := range rest.Groups {
			groupKey := GroupKey(e, group)
			w.Debug("processing group", wool.Field("key", groupKey), wool.SliceCountField(group.Routes))
			for _, r := range group.Routes {
				key := RouteUnique{
					service: e.Service,
					module:  e.Module,
					path:    r.Path,
					method:  ConvertHTTPMethodFromProto(r.Method),
				}
				if _, ok := knownRoutes[key.String()]; !ok {
					w.Debug("detected unknown route", wool.Field("route", key.String()))
					var outputGroup *RestRouteGroup
					var groupKnown bool
					if outputGroup, groupKnown = newGroups[groupKey]; !groupKnown {
						outputGroup = &RestRouteGroup{
							Module:  e.Module,
							Service: e.Service,
							Path:    r.Path,
						}
						newGroups[groupKey] = outputGroup
					}
					outputGroup.Routes = append(outputGroup.Routes, RestRouteFromProto(r))
				}
			}
		}

	}
	var output []*RestRouteGroup
	for _, g := range newGroups {
		w.Debug("new group", wool.ModuleField(g.Module), wool.ServiceField(g.Service), wool.Field("path", g.Path))
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
		if grpc := IsGRPC(ctx, e); grpc != nil {
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

// FindEndpointForRestRoute finds the info that matches the route rpcs
func FindEndpointForRestRoute(ctx context.Context, endpoints []*basev0.Endpoint, route *RestRouteGroup) *basev0.Endpoint {
	for _, e := range endpoints {
		if e.Module == route.Module && e.Service == route.Service && IsRest(ctx, e) != nil {
			return e
		}
	}
	return nil
}

// FindEndpointForGRPCRoute finds the info that matches the route rpcs
func FindEndpointForGRPCRoute(ctx context.Context, endpoints []*basev0.Endpoint, route *GRPCRoute) *basev0.Endpoint {
	for _, e := range endpoints {
		if e.Module == route.Module && e.Service == route.Service && IsGRPC(ctx, e) != nil {
			return e
		}
	}
	return nil
}
