package resources

import (
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// Environment is the runtime configuration context shared with agents.
type Environment struct {
	Name                 string                       `yaml:"name"`
	Description          string                       `yaml:"description,omitempty"`
	NamingScope          string                       `yaml:"naming-scope,omitempty"`
	Fixture              string                       `yaml:"fixture,omitempty"`
	ConfigurationProfile string                       `yaml:"configuration-profile,omitempty"`
	Secrets              []*EnvironmentSecretProvider `yaml:"secrets,omitempty"`

	// Extensions preserve host-owned declarations during workspace load/save.
	// Core neither interprets nor validates them. The consuming host must do so
	// before acting, and these fields are never transported to agents.
	Extensions map[string]any `yaml:",inline"`
}

func (env Environment) MarshalYAML() (any, error) {
	if err := validateExtensionKeys(env, env.Extensions); err != nil {
		return nil, err
	}
	type runtime Environment
	return runtime(env), nil
}

// EnvironmentSecretProvider configures one secret backend for an environment.
// Values in *.secret.ref.* manifests are references (op://…) resolved at Load
// time through the backend's CLI; nothing secret is written to disk. It is a
// list so more backends can be added later.
//
//	Kind:    "1password".
//	Account: 1Password account shorthand passed as `op --account`.
type EnvironmentSecretProvider struct {
	Kind    string `yaml:"kind"`
	Account string `yaml:"account,omitempty"`
}

func (env *Environment) Proto() (*basev0.Environment, error) {
	if env.ConfigurationProfile != "" {
		if err := validateResourcePathComponent("configuration profile", env.ConfigurationProfile); err != nil {
			return nil, err
		}
	}
	proto := &basev0.Environment{
		Name:        env.Name,
		Description: env.Description,
		NamingScope: env.NamingScope,
		Fixture:     env.Fixture,
	}
	err := Validate(proto)
	if err != nil {
		return nil, err
	}
	return proto, nil
}

func (env *Environment) ConfigurationProfileName() (string, error) {
	name := strings.TrimSpace(env.ConfigurationProfile)
	if name == "" {
		name = env.Name
	}
	if err := validateResourcePathComponent("configuration profile", name); err != nil {
		return "", err
	}
	return name, nil
}

func (env *Environment) Local() bool {
	return strings.HasPrefix(env.Name, "local")
}

func EnvironmentFromProto(env *basev0.Environment) *Environment {
	return &Environment{
		Name:        env.Name,
		Description: env.Description,
		NamingScope: env.NamingScope,
		Fixture:     env.Fixture,
	}
}

// An EnvironmentReference at the  level
type EnvironmentReference struct {
	Name string `yaml:"name"`
}

func (ref *EnvironmentReference) String() string {
	return ref.Name
}

// LocalEnvironment is the local runtime configuration context.
func LocalEnvironment() *Environment { return &Environment{Name: "local"} }
