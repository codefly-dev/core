package resources

import (
	"fmt"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// Environment is the runtime configuration context shared with agents.
type Environment struct {
	Name                 string `yaml:"name"`
	Description          string `yaml:"description,omitempty"`
	NamingScope          string `yaml:"naming-scope,omitempty"`
	Fixture              string `yaml:"fixture,omitempty"`
	ConfigurationProfile string `yaml:"configuration-profile,omitempty"`
	// ConfigurationProfiles is an ordered profile chain, the explicit
	// alternative to ConfigurationProfile (declaring both is an error). Every
	// configuration directory a run reads — the workspace's configurations/,
	// each composed workspace's and module's, each service's configurations/
	// and dns/ — is read from the FIRST profile in the chain that directory
	// holds, and from that one alone: profiles are never merged. So
	// [staging, local] reads a workspace's own configurations/staging while a
	// composed module that ships only configurations/local keeps supplying its
	// local defaults. Nothing falls back unless the environment says so.
	ConfigurationProfiles []string                     `yaml:"configuration-profiles,omitempty"`
	Secrets               []*EnvironmentSecretProvider `yaml:"secrets,omitempty"`

	// Extensions preserve host-owned declarations during workspace load/save.
	// Core neither interprets nor validates them. The consuming host must do so
	// before acting, and these fields are never transported to agents.
	Extensions map[string]YAMLValue `yaml:",inline"`
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
	if env.ConfigurationProfile != "" || len(env.ConfigurationProfiles) > 0 {
		if _, err := env.ConfigurationProfileNames(); err != nil {
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

// ConfigurationProfileName is the environment's own profile: the first of
// ConfigurationProfileNames. It is where configuration authored for this
// environment is written.
func (env *Environment) ConfigurationProfileName() (string, error) {
	names, err := env.ConfigurationProfileNames()
	if err != nil {
		return "", err
	}
	return names[0], nil
}

// ConfigurationProfileNames is the ordered profile chain a configuration
// directory is read through: ConfigurationProfiles when declared, otherwise
// the single ConfigurationProfile, otherwise the environment's name.
func (env *Environment) ConfigurationProfileNames() ([]string, error) {
	profile := strings.TrimSpace(env.ConfigurationProfile)
	if len(env.ConfigurationProfiles) > 0 {
		if profile != "" {
			return nil, fmt.Errorf("environment %q declares both configuration-profile and configuration-profiles; declare one", env.Name)
		}
		names := make([]string, 0, len(env.ConfigurationProfiles))
		seen := make(map[string]bool, len(env.ConfigurationProfiles))
		for _, name := range env.ConfigurationProfiles {
			name = strings.TrimSpace(name)
			if err := validateResourcePathComponent("configuration profile", name); err != nil {
				return nil, err
			}
			if seen[name] {
				return nil, fmt.Errorf("environment %q lists configuration profile %q twice", env.Name, name)
			}
			seen[name] = true
			names = append(names, name)
		}
		return names, nil
	}
	if profile == "" {
		profile = env.Name
	}
	if err := validateResourcePathComponent("configuration profile", profile); err != nil {
		return nil, err
	}
	return []string{profile}, nil
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
