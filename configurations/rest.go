package configurations

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	"github.com/codefly-dev/core/wool"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"

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

type RestRoute struct {
	Path        string
	Methods     []HTTPMethod
	Application string `yaml:"-"`
	Service     string `yaml:"-"`
}

func RouteUnique(endpoint *basev0.Endpoint, route *basev0.RestRoute) string {
	return MakeRouteUnique(endpoint.Application, endpoint.Service, route.Path)
}

func MakeRouteUnique(app string, service string, path string) string {
	if cut, ok := strings.CutPrefix(path, "/"); ok {
		path = cut
	}
	return fmt.Sprintf("%s/%s/%s", app, service, path)
}

func (r *RestRoute) Unique() string {
	return MakeRouteUnique(r.Application, r.Service, r.Path)
}

type ExtendedRestRoute[T any] struct {
	RestRoute `yaml:",inline"`

	Extension T `yaml:"extension"`
}

func NewExtendedRestRoute[T any](rest RestRoute, value T) *ExtendedRestRoute[T] {
	return &ExtendedRestRoute[T]{
		RestRoute: rest,
		Extension: value,
	}
}

func UnwrapRoute[T any](route *ExtendedRestRoute[T]) *RestRoute {
	return &route.RestRoute
}

func UnwrapRoutes[T any](routes []*ExtendedRestRoute[T]) []*RestRoute {
	var rs []*RestRoute
	for _, r := range routes {
		rs = append(rs, &r.RestRoute)
	}
	return rs
}

func (r *RestRoute) String() string {
	return fmt.Sprintf("%s/%s%s %s", r.Application, r.Service, r.Path, r.Methods)
}

type ApplicationRestRoute struct {
	ServiceRestRoutes []*ServiceRestRoute
	Name              string
}

type ServiceRestRoute struct {
	Routes      []*RestRoute
	Name        string
	Application string `yaml:"-"`
}

func sanitizeRoute(route string) string {
	route = strings.TrimPrefix(route, "/")
	return strings.ReplaceAll(route, "/", "_")
}

func sanitizePath(route string) string {
	route = strings.TrimPrefix(route, "/")
	return strings.ReplaceAll(route, "/", "#")
}

func (r *RestRoute) FilePath(ctx context.Context, dir string) (string, error) {
	dir = path.Join(dir, r.Application, r.Service)
	_, err := shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return "", err
	}
	file := path.Join(dir, fmt.Sprintf("%s.route.yaml", sanitizeRoute(r.Path)))
	return file, nil
}

// Save a route:
// The path is inferred from the configuration
func (r *RestRoute) Save(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("RestRoute::Save")
	file, err := r.FilePath(ctx, dir)
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
	out, err := yaml.Marshal(r)
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
func (r *RestRoute) Delete(ctx context.Context, dir string) error {
	w := wool.Get(ctx).In("RestRoute::Delete")
	file, err := r.FilePath(ctx, dir)
	if err != nil {
		return w.Wrapf(err, "cannot get file path for route to delete")
	}
	err = os.Remove(file)
	if err != nil {
		return w.Wrapf(err, "cannot delete route file")
	}
	return nil
}

func (r *ServiceRestRoute) Save(ctx context.Context, dir string) error {
	for _, route := range r.Routes {
		err := route.Save(ctx, dir)
		if err != nil {
			return err
		}
	}
	return nil
}

// Save as folder structure
func (r *ApplicationRestRoute) Save(ctx context.Context, dir string) error {
	for _, s := range r.ServiceRestRoutes {
		err := s.Save(ctx, dir)
		if err != nil {
			return err
		}
	}
	return nil
}

func LoadApplicationRoutes(ctx context.Context, dir string) ([]*RestRoute, error) {
	var routes []*RestRoute
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		r, err := LoadServiceRoutes(ctx, path.Join(dir, name), entry.Name())
		if err != nil {
			return nil, err
		}
		routes = append(routes, r...)
	}
	return routes, nil
}

func LoadServiceRoutes(ctx context.Context, dir string, app string) ([]*RestRoute, error) {
	var routes []*RestRoute
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		r, err := LoadRoutes(ctx, path.Join(dir, name), app, name)
		if err != nil {
			return nil, err
		}
		routes = append(routes, r...)
	}
	return routes, nil
}

func LoadRoutes(ctx context.Context, dir string, app string, service string) ([]*RestRoute, error) {
	var routes []*RestRoute
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), "route.yaml") {
			continue
		}
		r, err := LoadRoute(ctx, path.Join(dir, entry.Name()), app, service)
		if err != nil {
			return nil, err
		}
		routes = append(routes, r)
	}
	return routes, nil
}

func LoadRoute(ctx context.Context, p string, app string, service string) (*RestRoute, error) {
	r, err := LoadFromPath[RestRoute](ctx, p)
	if err != nil {
		return nil, err
	}
	r.Application = app
	r.Service = service
	return r, nil
}

// Extension of routes -- can we merge both?

func LoadApplicationExtendedRoutes[T any](_ context.Context, dir string) ([]*ExtendedRestRoute[T], error) {
	var routes []*ExtendedRestRoute[T]
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		r, err := LoadServiceExtendedRoutes[T](path.Join(dir, name), entry.Name())
		if err != nil {
			return nil, err
		}
		routes = append(routes, r...)
	}
	return routes, nil
}

func LoadServiceExtendedRoutes[T any](dir string, app string) ([]*ExtendedRestRoute[T], error) {
	var routes []*ExtendedRestRoute[T]
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		name := entry.Name()
		r, err := LoadExtendedRoutes[T](path.Join(dir, name), app, name)
		if err != nil {
			return nil, err
		}
		routes = append(routes, r...)
	}
	return routes, nil
}

func LoadExtendedRoutes[T any](dir string, app string, service string) ([]*ExtendedRestRoute[T], error) {
	var routes []*ExtendedRestRoute[T]
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if !strings.HasSuffix(entry.Name(), "route.yaml") {
			continue
		}
		r, err := LoadExtendedRestRoute[T](path.Join(dir, entry.Name()), app, service)
		if err != nil {
			return nil, err
		}
		routes = append(routes, r)
	}
	return routes, nil
}

func LoadExtendedRestRoute[T any](p string, app string, service string) (*ExtendedRestRoute[T], error) {
	content, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	var r ExtendedRestRoute[T]
	err = yaml.Unmarshal(content, &r)
	if err != nil {
		return nil, err
	}
	r.Application = app
	r.Service = service
	return &r, nil
}

func AsRestRouteEnvironmentVariable(endpoint *basev0.Endpoint) []string {
	var envs []string
	if rest := HasRest(context.Background(), endpoint.Api); rest != nil {
		for _, route := range rest.Routes {
			envs = append(envs, RestRoutesAsEnvironmentVariable(endpoint, route))
		}
	}
	return envs

}

/* For runtime */

const RestRoutePrefix = "CODEFLY_RESTROUTE__"

func RestRoutesAsEnvironmentVariable(endpoint *basev0.Endpoint, route *basev0.RestRoute) string {
	return fmt.Sprintf("%s=%s", RestRouteEnvironmentVariableKey(endpoint, route), serializeMethods(route.Methods))
}

func serializeMethods(methods []basev0.HTTPMethod) string {
	var ss []string
	for _, m := range methods {
		ss = append(ss, m.String())
	}
	return strings.Join(ss, ",")

}

func RestRouteEnvironmentVariableKey(endpoint *basev0.Endpoint, route *basev0.RestRoute) string {
	unique := FromProtoEndpoint(endpoint).Unique()
	unique = strings.ToUpper(unique)
	unique = strings.Replace(unique, "/", "__", 1)
	unique = strings.Replace(unique, "/", "___", 1)
	unique = strings.Replace(unique, "::", "____", 1)
	// Add path
	unique = fmt.Sprintf("%s____%s", unique, sanitizePath(route.Path))
	return strings.ToUpper(fmt.Sprintf("%s%s", RestRoutePrefix, unique))
}

func ContainsRoute(routes []*RestRoute, r *RestRoute) bool {
	for _, route := range routes {
		if route.Application == r.Application && route.Service == r.Service && route.Path == r.Path {
			return true
		}
	}
	return false
}

func ConvertRoutes(routes []*basev0.RestRoute, app string, service string) []*RestRoute {
	var rs []*RestRoute
	for _, r := range routes {
		rs = append(rs, &RestRoute{
			Path:        r.Path,
			Methods:     ConvertMethods(r.Methods),
			Application: app,
			Service:     service,
		})
	}
	return rs
}

func ConvertMethods(methods []basev0.HTTPMethod) []HTTPMethod {
	var ms []HTTPMethod
	for _, m := range methods {
		ms = append(ms, ConvertMethod(m))
	}
	return ms
}

func ConvertMethod(m basev0.HTTPMethod) HTTPMethod {
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
