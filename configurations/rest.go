package configurations

import (
	"fmt"
	"github.com/codefly-dev/core/shared"
	"gopkg.in/yaml.v3"
	"os"
	"path"
	"strings"
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

func (r *RestRoute) Save(dir string, logger shared.BaseLogger) error {
	dir = path.Join(dir, r.Application, r.Service)
	err := shared.CheckDirectoryOrCreate(dir)
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

func (r *ServiceRestRoute) Save(dir string) error {
	for _, route := range r.Routes {
		err := route.Save(dir, nil)
		if err != nil {
			return err
		}
	}
	return nil
}

// Save as folder structure
func (r *ApplicationRestRoute) Save(dir string, logger shared.BaseLogger) error {
	logger.DebugMe("Saving application rest route to %s", dir)
	for _, s := range r.ServiceRestRoutes {
		err := s.Save(dir)
		if err != nil {
			return err
		}
	}
	return nil
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
