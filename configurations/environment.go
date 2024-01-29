package configurations

import (
	"context"
	"fmt"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

/*
An environment is where your applications are deployed.

It exists at the project level.
*/

var NetworkPort string
var NetworkDNS string

func init() {
	NetworkPort = basev0.Environment_NetworkType_name[int32(basev0.Environment_PORT)]
	NetworkDNS = basev0.Environment_NetworkType_name[int32(basev0.Environment_DNS)]
}

type EnvironmentExistsError struct {
	name string
}

func (err *EnvironmentExistsError) Error() string {
	return fmt.Sprintf("environment %s already exists", err.name)
}

// Environment is a configuration for an environment
type Environment struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
	NetworkType string `yaml:"networkType,omitempty"`
}

func (env *Environment) Proto() (*basev0.Environment, error) {
	if networkType, ok := basev0.Environment_NetworkType_value[env.NetworkType]; ok {
		proto := &basev0.Environment{
			Name:        env.Name,
			Description: env.Description,
			NetworkType: basev0.Environment_NetworkType(networkType),
		}
		err := Validate(proto)
		if err != nil {
			return nil, err
		}
		return proto, nil
	}
	return nil, fmt.Errorf("invalid network type <%s>, available: %v", env.NetworkType, basev0.Environment_NetworkType_name)
}

func EnvironmentFromProto(environment *basev0.Environment) *Environment {
	return &Environment{
		Name:        environment.Name,
		Description: environment.Description,
		NetworkType: string(environment.NetworkType),
	}
}

// An EnvironmentReference at the Project level
type EnvironmentReference struct {
	Name string `yaml:"name"`
}

func (ref *EnvironmentReference) String() string {
	return ref.Name
}

func (project *Project) NewEnvironment(_ context.Context, input *actionsv0.AddEnvironment) (*Environment, error) {
	for _, env := range project.Environments {
		if env.Name == input.Name {
			return nil, &EnvironmentExistsError{name: input.Name}
		}
	}
	env := &Environment{
		Name:        input.Name,
		Description: input.Description,
		NetworkType: input.NetworkType,
	}
	project.Environments = append(project.Environments, &EnvironmentReference{Name: env.Name})
	return env, nil
}

// Local is a local environment that is always available
func Local() *Environment {
	return &Environment{
		Name:        "local",
		NetworkType: basev0.Environment_NetworkType_name[int32(basev0.Environment_PORT)],
	}
}

func EnvironmentAsEnvironmentVariable(env *Environment) string {
	return fmt.Sprintf("CODEFLY_ENVIRONMENT=%s", env.Name)
}
