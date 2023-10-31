package configurations

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/codefly-dev/core/shared"
)

type Plugin struct {
	Kind       string `yaml:"kind"`
	Identifier string `yaml:"name"`
	Version    string `yaml:"version"`
	Publisher  string `yaml:"publisher"`
}

func NewPlugin(kind string, publisher string, identifier string, version string) *Plugin {
	p := &Plugin{
		Kind:       kind,
		Publisher:  publisher,
		Identifier: identifier,
		Version:    version,
	}
	p.Validate()
	return p
}

func (p *Plugin) Validate() {
	if p.Publisher == "" {
		shared.Exit("plugin publisher is required")
	}
	if p.Identifier == "" {
		shared.Exit("plugin identifier is required")
	}
	if p.Version == "" {
		shared.Exit("plugin version is required")
	}
	if p.Kind == "" {
		shared.Exit("plugin kind is required")
	}
}

func KnownPluginImplementationKinds() []string {
	return []string{PluginLibrary, PluginRuntimeService, PluginFactoryService}
}

const (
	PluginService = "service"
	PluginLibrary = "library"
)

const (
	PluginRuntimeService = "runtime::service"
	PluginFactoryService = "factory::service"
)

const PluginProvider = "provider"

func (p *Plugin) ImplementationKind() string {
	if !slices.Contains(KnownPluginImplementationKinds(), p.Kind) {
		shared.Exit("unknown plugin kind: %s", p.Kind)
	}
	return p.Kind
}

func (p *Plugin) Name() string {
	return fmt.Sprintf("%s/%s:%s", p.Publisher, p.Identifier, p.Version)
}

func (p *Plugin) Key(f string) string {
	return fmt.Sprintf("%s::%s", f, p.Name())
}

func (p *Plugin) Unique() string {
	return fmt.Sprintf("%s::%s", p.Kind, p.Name())
}

func (p *Plugin) Of(kind string) *Plugin {
	out := Plugin{
		Kind:       kind,
		Publisher:  p.Publisher,
		Identifier: p.Identifier,
		Version:    p.Version,
	}
	return &out
}

func (p *Plugin) Path() (string, error) {
	var subdir string
	switch p.Kind {
	case PluginLibrary:
		subdir = "libraries"
	case PluginRuntimeService:
		subdir = "services"
	case PluginFactoryService:
		subdir = "services"
	default:
		return "", fmt.Errorf("unknown kind: %s", p.Kind)
	}
	return path.Join(GlobalConfigurationDir(), "plugins", subdir, p.Name()), nil
}

func ParsePlugin(kind string, s string) (*Plugin, error) {
	var err error
	// TODO: More validation
	tokens := strings.SplitN(s, "/", 2)
	pub := "codefly.ai"
	rest := s
	if len(tokens) == 2 {
		pub = tokens[0]
		rest = tokens[1]
	}
	tokens = strings.Split(rest, ":")
	if len(tokens) > 2 || len(tokens) == 0 {
		return nil, fmt.Errorf("invalid plugin Name (should be of the form identifier:version): %s", s)
	}
	identifier := tokens[0]
	version := "latest"
	if len(tokens) == 1 {
		err = shared.NewUserWarning("No version of the plugin <%s> specified, assuming latest", identifier)
	} else {
		version = tokens[1]
	}
	return &Plugin{Kind: kind, Publisher: pub, Identifier: identifier, Version: version}, err
}
