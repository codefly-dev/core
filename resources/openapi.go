package resources

import (
	"context"
	"fmt"
	"os"
	"slices"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
	"github.com/go-openapi/spec"
)

type OpenAPICombinator struct {
	endpoint *Endpoint
	unique   string

	openapis []*WrapperSwagger
	filename string
	only     map[string][]string
	version  string
}

func (c *OpenAPICombinator) WithDestination(filename string) {
	c.filename = filename
}

func (c *OpenAPICombinator) WithVersion(version string) {
	c.version = version
}

type WrapperSwagger struct {
	swagger *spec.Swagger
	unique  string
}

func NewOpenAPICombinator(ctx context.Context, target *Endpoint, endpoints ...*basev0.Endpoint) (*OpenAPICombinator, error) {
	w := wool.Get(ctx).In("configurations.NewOpenAPICombinator")
	c := &OpenAPICombinator{endpoint: target, only: make(map[string][]string)}
	c.unique = ServiceUnique(target.Module, target.Service)
	err := c.LoadEndpoints(ctx, endpoints...)
	if err != nil {
		return nil, w.Wrapf(err, "failed to load endpoints")
	}
	return c, nil
}

func (c *OpenAPICombinator) LoadEndpoints(ctx context.Context, endpoints ...*basev0.Endpoint) error {
	w := wool.Get(ctx).In("configurations.LoadEndpoints")
	// Get the Spec
	var openapis []*WrapperSwagger
	for _, endpoint := range endpoints {
		rest := EndpointRestAPI(endpoint)
		if rest == nil || rest.Openapi == nil {
			continue
		}
		swagger, err := ParseOpenAPI(rest.Openapi)
		if err != nil {
			return w.Wrapf(err, "failed to parse openapi spec")
		}
		openapis = append(openapis, &WrapperSwagger{swagger: swagger, unique: ServiceUnique(endpoint.Module, endpoint.Service)})
	}
	c.openapis = openapis
	return nil
}

func (c *OpenAPICombinator) Combine(ctx context.Context) (*basev0.RestAPI, error) {
	w := wool.Get(ctx).In("configurations.CombineOpenAPI")

	combined := &spec.Swagger{
		SwaggerProps: spec.SwaggerProps{
			Swagger: "2.0",
			Info: &spec.Info{
				InfoProps: spec.InfoProps{
					Title:   c.unique,
					Version: c.version,
				},
			},
			Paths: &spec.Paths{Paths: map[string]spec.PathItem{}},
		},
	}

	// Iterate over each document
	for _, s := range c.openapis {
		// Combine paths
		//nolint:gocritic
		for path, pathItem := range s.swagger.Paths.Paths {
			if only, ok := c.only[s.unique]; ok {
				if !slices.Contains(only, path) {
					continue
				}
			}
			// New path
			path = fmt.Sprintf("/%s%s", s.unique, path)
			if _, exists := combined.Paths.Paths[path]; exists {
				// Handle path conflict
				return nil, fmt.Errorf("path conflict: %s", path)
			}

			combined.Paths.Paths[path] = pathItem
			continue
		}

		// Combine definitions (schemas)
		if combined.Definitions == nil {
			combined.Definitions = make(spec.Definitions)
		}
		//nolint:gocritic
		for name, definition := range s.swagger.Definitions {
			if _, exists := combined.Definitions[name]; exists {
				// Handle definition conflict
				return nil, fmt.Errorf("definition conflict: %s", name)
			}
			combined.Definitions[name] = definition
		}

		// Similarly, combine other components if needed (responses, parameters, etc.)
		// ...
	}
	// Write to file
	out, err := combined.MarshalJSON()
	if err != nil {
		return nil, w.Wrapf(err, "failed to marshal combined openapi spec")
	}
	if c.filename == "" {
		return nil, fmt.Errorf("filename not set")
	}
	err = writeToFile(c.filename, out)
	if err != nil {
		return nil, w.Wrapf(err, "failed to write combined openapi spec to file")
	}

	rest, err := LoadRestAPI(ctx, shared.Pointer(c.filename))
	if err != nil {
		return nil, w.Wrapf(err, "cannot create REST endpoint from filename")
	}
	return rest, nil
}

func (c *OpenAPICombinator) Only(unique string, path string) {
	c.only[unique] = append(c.only[unique], path)
}

func writeToFile(destination string, out []byte) error {
	return os.WriteFile(destination, out, 0600)
}
