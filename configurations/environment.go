package configurations

/*
An environment is where your applications are deployed.

It exists at the project level.
*/

// Environment is a configuration for an environment
type Environment struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description,omitempty"`
}

func NewEnvironment(name string) *Environment {
	return &Environment{
		Name: name,
	}
}

// Local is a local environment that is always available
func Local() *Environment {
	return &Environment{
		Name: "local",
	}
}
