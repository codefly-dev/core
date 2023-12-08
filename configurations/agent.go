package configurations

import (
	"context"
	"fmt"
	"github.com/bufbuild/protovalidate-go"

	basev1 "github.com/codefly-dev/core/proto/v1/go/base"
	"path"
	"slices"
	"strings"

	"github.com/Masterminds/semver"

	"github.com/codefly-dev/core/shared"
)

const AgentConfigurationName = "agent.codefly.yaml"

type Agent struct {
	Kind      string `yaml:"kind"`
	Name      string `yaml:"name"`
	Version   string `yaml:"version"`
	Publisher string `yaml:"publisher"`
}

func RegisterAgent(kind string, protoKind basev1.Agent_Kind) {
	agentKinds[protoKind] = kind
	agentInputs[kind] = protoKind
}

var agentKinds map[basev1.Agent_Kind]string
var agentInputs map[string]basev1.Agent_Kind

func init() {
	agentKinds = map[basev1.Agent_Kind]string{}
	agentInputs = map[string]basev1.Agent_Kind{}
}

func (p *Agent) String() string {
	return fmt.Sprintf("%s/%s:%s", p.Publisher, p.Name, p.Version)
}

func LoadAgent(ctx context.Context, action *basev1.Agent) (*Agent, error) {
	logger := shared.GetBaseLogger(ctx).With("LoadAgent")
	if err := ValidateAgent(action); err != nil {
		return nil, logger.Wrapf(err, "invalid agent")
	}
	p := &Agent{
		Kind:      agentKinds[action.Kind],
		Publisher: action.Publisher,
		Name:      action.Name,
		Version:   action.Version,
	}
	return p, nil
}

func AgentKindFromProto(kind basev1.Agent_Kind) (*string, error) {
	s, ok := agentKinds[kind]
	if !ok {
		return nil, fmt.Errorf("unknown agent kind: %s", kind)
	}
	return &s, nil
}

func AgentKind(kind string) (basev1.Agent_Kind, error) {
	k, ok := agentInputs[kind]
	if !ok {
		return basev1.Agent_UNKNOWN, fmt.Errorf("unknown agent kind: %s", kind)
	}
	return k, nil
}

func ValidateAgent(agent *basev1.Agent) error {
	if agent == nil {
		return shared.NewNilError[Agent]()
	}
	v, err := protovalidate.New()
	if err != nil {
		return err
	}
	if err = v.Validate(agent); err != nil {
		return err
	}
	return nil
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

func (p *Agent) Identifier() string {
	return fmt.Sprintf("%s/%s:%s", p.Publisher, p.Name, p.Version)
}

func (p *Agent) Key(f string, unique string) string {
	return fmt.Sprintf("%s::%s::%s", f, p.Identifier(), unique)
}

func (p *Agent) Unique() string {
	return fmt.Sprintf("%s::%s", p.Kind, p.Identifier())
}

func (p *Agent) Of(kind string) *Agent {
	out := Agent{
		Kind:      kind,
		Publisher: p.Publisher,
		Name:      p.Name,
		Version:   p.Version,
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
	name := p.Identifier()
	// Replace : by __ for compatibility of file names
	name = strings.Replace(name, ":", "__", 1)
	return path.Join(WorkspaceConfigurationDir(), "agents", subdir, name), nil
}

func (p *Agent) Patch() (*Agent, error) {
	patch := &Agent{
		Kind:      p.Kind,
		Publisher: p.Publisher,
		Name:      p.Name,
	}

	v, err := semver.NewVersion(p.Version)
	if err != nil {
		return nil, err
	}
	n := v.IncPatch()
	patch.Version = n.String()
	return patch, nil
}

func ParseAgent(ctx context.Context, kindInput string, s string) (*basev1.Agent, error) {
	logger := shared.GetBaseLogger(ctx).With("ParseAgent<%s>", s)
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
		logger.Warn("missing version, assuming latest")
	} else {
		version = tokens[1]
	}
	kind, err := AgentKind(kindInput)

	agent := &basev1.Agent{Kind: kind, Publisher: pub, Name: identifier, Version: version}
	v, err := protovalidate.New()
	if err != nil {
		return nil, err
	}
	if err = v.Validate(agent); err != nil {
		return nil, err
	}
	return agent, nil
}

func AgentFromProto(agent *basev1.Agent) *Agent {
	return &Agent{
		Kind:      agentKinds[agent.Kind],
		Publisher: agent.Publisher,
		Name:      agent.Name,
		Version:   agent.Version,
	}

}
