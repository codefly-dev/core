package configurations

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	basev1 "github.com/codefly-dev/core/generated/go/base/v1"

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

func sanitize(route string) string {
	route = strings.TrimPrefix(route, "/")
	return strings.ReplaceAll(route, "/", "_")
}

func (r *RestRoute) FilePath(ctx context.Context, dir string) (string, error) {
	dir = path.Join(dir, r.Application, r.Service)
	err := shared.CheckDirectoryOrCreate(ctx, dir)
	if err != nil {
		return "", err
	}
	file := path.Join(dir, fmt.Sprintf("%s.route.yaml", sanitize(r.Path)))
	return file, nil
}

// Save a route:
// The path is inferred from the configuration
func (r *RestRoute) Save(ctx context.Context, dir string) error {
	logger := shared.GetAgentLogger(ctx)
	file, err := r.FilePath(ctx, dir)
	if err != nil {
		return logger.Wrapf(err, "cannot get file path for route to save")
	}
	logger.Debugf("Saving rest route to %s", file)
	f, err := os.Create(file)
	if err != nil {
		return logger.Wrapf(err, "cannot create file for route")
	}
	defer f.Close()
	out, err := yaml.Marshal(r)
	if err != nil {
		return err
	}
	_, err = f.Write(out)
	if err != nil {
		return err
	}
	return nil
}

// Delete a route
func (r *RestRoute) Delete(ctx context.Context, dir string) error {
	logger := shared.GetAgentLogger(ctx)
	file, err := r.FilePath(ctx, dir)
	if err != nil {
		return logger.Wrapf(err, "cannot get file path for route to delete")
	}
	err = os.Remove(file)
	if err != nil {
		return logger.Wrapf(err, "cannot delete route file")
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

/* For runtime */

const RestRoutePrefix = "CODEFLY-RESTROUTE_"

func RestRouteAsEnvironmentVariable(reference string, addresses []string) string {
	return fmt.Sprintf("%s%s=%s", RestRoutePrefix, strings.ToUpper(reference), SerializeAddresses(addresses))
}

func ParseRestRouteEnvironmentVariable(env string) (string, []string) {
	tokens := strings.Split(env, "=")
	reference := strings.ToLower(tokens[0])
	// Namespace break
	reference = strings.Replace(reference, "_", ".", 1)
	reference = strings.Replace(reference, "_", "::", 1)
	values := strings.Split(tokens[1], " ")
	return reference, values
}

func DetectNewRoutes(known []*RestRoute, routes []*RestRoute) []*RestRoute {
	var rs []*RestRoute
	for _, r := range routes {
		if !ContainsRoute(known, r) {
			rs = append(rs, r)
		}
	}
	return rs
}

func ContainsRoute(routes []*RestRoute, r *RestRoute) bool {
	for _, route := range routes {
		if route.Application == r.Application && route.Service == r.Service && route.Path == r.Path {
			return true
		}
	}
	return false
}

func ConvertRoutes(routes []*basev1.RestRoute, app string, service string) []*RestRoute {
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

func ConvertMethods(methods []basev1.HTTPMethod) []HTTPMethod {
	var ms []HTTPMethod
	for _, m := range methods {
		ms = append(ms, ConvertMethod(m))
	}
	return ms
}

func ConvertMethod(m basev1.HTTPMethod) HTTPMethod {
	switch m {
	case basev1.HTTPMethod_GET:
		return HTTPMethodGet
	case basev1.HTTPMethod_POST:
		return HTTPMethodPost
	case basev1.HTTPMethod_PUT:
		return HTTPMethodPut
	case basev1.HTTPMethod_DELETE:
		return HTTPMethodDelete
	case basev1.HTTPMethod_PATCH:
		return HTTPMethodPatch
	case basev1.HTTPMethod_OPTIONS:
		return HTTPMethodOptions
	case basev1.HTTPMethod_HEAD:
		return HTTPMethodHead
	}
	panic(fmt.Sprintf("unknown HTTP method: <%v>", m))
}
