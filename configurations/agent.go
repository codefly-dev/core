package configurations

import (
	"fmt"
	"path"
	"slices"
	"strings"

	"github.com/Masterminds/semver"

	"github.com/codefly-dev/core/shared"
)

const AgentConfigurationName = "agent.codefly.yaml"

type Agent struct {
	Kind       string `yaml:"kind"`
	Identifier string `yaml:"name"`
	Version    string `yaml:"version"`
	Publisher  string `yaml:"publisher"`
}

func (p *Agent) String() string {
	return fmt.Sprintf("%s/%s:%s", p.Publisher, p.Identifier, p.Version)
}

func NewAgent(kind string, publisher string, identifier string, version string) *Agent {
	p := &Agent{
		Kind:       kind,
		Publisher:  publisher,
		Identifier: identifier,
		Version:    version,
	}
	p.Validate()
	return p
}

func LoadAgentConfiguration(fs shared.FileSystem) *Agent {
	content, err := fs.ReadFile(shared.NewFile(AgentConfigurationName))
	if err != nil {
		shared.ExitOnError(err, "cannot load agent configurations")
	}
	conf, err := LoadFromBytes[Agent](content)
	if err != nil {
		shared.ExitOnError(err, "cannot load agent configurations")
	}
	return conf
}

func LoadAgentConfigurationFromReader(fs shared.FSReader) Agent {
	content, err := fs.ReadFile(shared.NewFile(AgentConfigurationName))
	if err != nil {
		shared.ExitOnError(err, "cannot load agent configurations")
	}
	conf, err := LoadFromBytes[Agent](content)
	if err != nil {
		shared.ExitOnError(err, "cannot load agent configurations")
	}
	return *conf
}

func (p *Agent) Validate() {
	if p.Publisher == "" {
		shared.Exit("agent publisher is required")
	}
	if p.Identifier == "" {
		shared.Exit("agent identifier is required")
	}
	if p.Version == "" {
		shared.Exit("agent version is required")
	}
	if p.Kind == "" {
		shared.Exit("agent kind is required")
	}
}

func KnownAgentImplementationKinds() []string {
	return []string{AgentLibrary, AgentRuntimeService, AgentFactoryService}
}

const (
	AgentService = "service"
	AgentLibrary = "library"
)

const (
	AgentRuntimeService = "runtime::service"
	AgentFactoryService = "factory::service"
)

const AgentProvider = "provider"

func (p *Agent) ImplementationKind() string {
	if !slices.Contains(KnownAgentImplementationKinds(), p.Kind) {
		shared.Exit("unknown agent kind: %s", p.Kind)
	}
	return p.Kind
}

func (p *Agent) Name() string {
	return fmt.Sprintf("%s/%s:%s", p.Publisher, p.Identifier, p.Version)
}

func (p *Agent) Key(f string, unique string) string {
	return fmt.Sprintf("%s::%s::%s", f, p.Name(), unique)
}

func (p *Agent) Unique() string {
	return fmt.Sprintf("%s::%s", p.Kind, p.Name())
}

func (p *Agent) Of(kind string) *Agent {
	out := Agent{
		Kind:       kind,
		Publisher:  p.Publisher,
		Identifier: p.Identifier,
		Version:    p.Version,
	}
	return &out
}

func (p *Agent) Path() (string, error) {
	var subdir string
	switch p.Kind {
	case AgentLibrary:
		subdir = "libraries"
	case AgentRuntimeService:
		subdir = "services"
	case AgentFactoryService:
		subdir = "services"
	default:
		return "", fmt.Errorf("unknown kind: %s", p.Kind)
	}
	name := p.Name()
	// Replace : by __ for compatilbity of file names
	name = strings.Replace(name, ":", "__", 1)
	return path.Join(GlobalConfigurationDir(), "agents", subdir, name), nil
}

func (p *Agent) Patch() (*Agent, error) {
	patch := &Agent{
		Kind:       p.Kind,
		Publisher:  p.Publisher,
		Identifier: p.Identifier,
	}

	v, err := semver.NewVersion(p.Version)
	if err != nil {
		return nil, err
	}
	n := v.IncPatch()
	patch.Version = n.String()
	return patch, nil
}

func ParseAgent(kind string, s string) (*Agent, error) {
	var err error
	// TODO: More validation
	tokens := strings.SplitN(s, "/", 2)
	pub := "codefly.dev"
	rest := s
	if len(tokens) == 2 {
		pub = tokens[0]
		rest = tokens[1]
	}
	tokens = strings.Split(rest, ":")
	if len(tokens) > 2 || len(tokens) == 0 {
		return nil, fmt.Errorf("invalid agent Name (should be of the form identifier:version): %s", s)
	}
	identifier := tokens[0]
	version := "latest"
	if len(tokens) == 1 {
		err = shared.NewUserWarning("No version of the agent <%s> specified, assuming latest", identifier)
	} else {
		version = tokens[1]
	}
	return &Agent{Kind: kind, Publisher: pub, Identifier: identifier, Version: version}, err
}
