package configurations

import (
	"context"
	"fmt"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
)

/*
An environment is where your applications are deployed.

It exists at the project level.
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
	}
	project.Environments = append(project.Environments, &EnvironmentReference{Name: env.Name})
	return env, nil
}

// Local is a local environment that is always available
func Local() *Environment {
	return &Environment{
		Name: "local",
	}
}
