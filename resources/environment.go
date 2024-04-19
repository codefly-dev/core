package resources

import (
	"fmt"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
)

/*
An environment is where your modules are deployed.

It exists at the  level.
*/

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
}

func (env *Environment) Proto() (*basev0.Environment, error) {
	proto := &basev0.Environment{
		Name:        env.Name,
		Description: env.Description,
	}
	err := Validate(proto)
	if err != nil {
		return nil, err
	}
	return proto, nil
}

func (env *Environment) Local() bool {
	return env.Name == "local"
}

func EnvironmentFromProto(env *basev0.Environment) *Environment {
	return &Environment{
		Name:        env.Name,
		Description: env.Description,
	}
}

// An EnvironmentReference at the  level
type EnvironmentReference struct {
	Name string `yaml:"name"`
}

func (ref *EnvironmentReference) String() string {
	return ref.Name
}

// LocalEnvironment is a local environment that is always available
func LocalEnvironment() *Environment {
	return &Environment{
		Name: "local",
	}
}
