package configurations

import (
	"context"
	"fmt"
	"os"
	"path"
	"strings"

	basev1 "github.com/codefly-dev/core/proto/v1/go/base"

	"github.com/codefly-dev/core/shared"
	"gopkg.in/yaml.v3"
)

type HttpMethod string

const (
	HttpMethodGet     HttpMethod = "GET"
	HttpMethodPut     HttpMethod = "PUT"
	HttpMethodPost    HttpMethod = "POST"
	HttpMethodDelete  HttpMethod = "DELETE"
	HttpMethodPatch   HttpMethod = "PATCH"
	HttpMethodOptions HttpMethod = "OPTIONS"
	HttpMethodHead    HttpMethod = "HEAD"
)

type RestRoute struct {
	Path        string
	Methods     []HttpMethod
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

func UnwrapRoutes[T any](routes []*ExtendedRestRoute[T]) []*RestRoute {
	var rs []*RestRoute
	for _, r := range routes {
		rs = append(rs, &r.RestRoute)
	}
	return rs
}

func (r *RestRoute) String() string {
	return fmt.Sprintf("%s.%s%s %s", r.Service, r.Application, r.Path, r.Methods)
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

// Save a route:
// The path is inferred from the configuration
// application
//
//	 service
//		path.codefly.route.yaml
func (r *RestRoute) Save(ctx context.Context, dir string) error {
	logger := ctx.Value(shared.Agent).(shared.BaseLogger)
	dir = path.Join(dir, r.Application, r.Service)
	logger.DebugMe("saving rest route %v to %s", r, dir)
	err := shared.CheckDirectoryOrCreate(dir)
	if err != nil {
		return err
	}
	file := path.Join(dir, fmt.Sprintf("%s.route.yaml", sanitize(r.Path)))
	logger.DebugMe("Saving rest route to %s", file)
	f, err := os.Create(file)
	if err != nil {
		return err
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

func LoadApplicationRoutes(dir string) ([]*RestRoute, error) {
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
		r, err := LoadServiceRoutes(path.Join(dir, name), entry.Name())
		if err != nil {
			return nil, err
		}
		routes = append(routes, r...)
	}
	return routes, nil
}

func LoadServiceRoutes(dir string, app string) ([]*RestRoute, error) {
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
		r, err := LoadRoutes(path.Join(dir, name), app, name)
		if err != nil {
			return nil, err
		}
		routes = append(routes, r...)
	}
	return routes, nil
}

func LoadRoutes(dir string, app string, service string) ([]*RestRoute, error) {
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
		r, err := LoadRoute(path.Join(dir, entry.Name()), app, service)
		if err != nil {
			return nil, err
		}
		routes = append(routes, r)
	}
	return routes, nil
}

func LoadRoute(p string, app string, service string) (*RestRoute, error) {
	r, err := LoadFromPath[RestRoute](p)
	if err != nil {
		return nil, err
	}
	r.Application = app
	r.Service = service
	return r, nil
}

// Extension of routes -- can we merge both?

func LoadApplicationExtendedRoutes[T any](dir string, logger shared.BaseLogger) ([]*ExtendedRestRoute[T], error) {
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

func ConvertMethods(methods []basev1.HttpMethod) []HttpMethod {
	var ms []HttpMethod
	for _, m := range methods {
		ms = append(ms, ConvertMethod(m))
	}
	return ms
}

func ConvertMethod(m basev1.HttpMethod) HttpMethod {
	switch m {
	case basev1.HttpMethod_GET:
		return HttpMethodGet
	case basev1.HttpMethod_POST:
		return HttpMethodPost
	case basev1.HttpMethod_PUT:
		return HttpMethodPut
	case basev1.HttpMethod_DELETE:
		return HttpMethodDelete
	case basev1.HttpMethod_PATCH:
		return HttpMethodPatch
	case basev1.HttpMethod_OPTIONS:
		return HttpMethodOptions
	case basev1.HttpMethod_HEAD:
		return HttpMethodHead
	}
	panic(fmt.Sprintf("unknown http method: <%v>", m))
}
