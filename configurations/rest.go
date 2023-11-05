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
	Path    string
	Methods []HttpMethod
}

type ApplicationRestRoute struct {
	ServiceRestRoutes []*ServiceRestRoute
	Name              string
}

type ServiceRestRoute struct {
	Routes []*RestRoute
	Name   string
}

func sanitize(route string) string {
	if strings.HasPrefix(route, "/") {
		route = route[1:]
	}
	return strings.ReplaceAll(route, "/", "_")
}

func (r ServiceRestRoute) Save(dir string) error {
	for _, route := range r.Routes {
		file := path.Join(dir, fmt.Sprintf("%s.yaml", sanitize(route.Path)))
		f, err := os.Create(file)
		if err != nil {
			return err
		}
		defer f.Close()
		out, err := yaml.Marshal(route)
		if err != nil {
			return err
		}
		_, err = f.Write(out)
		if err != nil {
			return err
		}
	}
	return nil
}

// Save as folder structure
func (r *ApplicationRestRoute) Save(location string, logger shared.BaseLogger) error {
	dir := path.Join(location, r.Name)
	logger.DebugMe("Saving application rest route to %s", dir)
	err := shared.CheckDirectoryOrCreate(dir)
	if err != nil {
		return err
	}
	for _, s := range r.ServiceRestRoutes {
		serviceDir := path.Join(dir, s.Name)
		err := shared.CheckDirectoryOrCreate(serviceDir)
		if err != nil {
			return err
		}
		err = s.Save(serviceDir)
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
