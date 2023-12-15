package configurations

import (
	"context"
	"fmt"
	"slices"

	"github.com/bufbuild/protovalidate-go"

	"path"
	"strings"

	basev1 "github.com/codefly-dev/core/generated/go/base/v1"

	"github.com/Masterminds/semver"

	"github.com/codefly-dev/core/shared"
)

const AgentConfigurationName = "agent.codefly.yaml"

type AgentKind string

type Agent struct {
	Kind      AgentKind `yaml:"kind"`
	Name      string    `yaml:"name"`
	Version   string    `yaml:"version"`
	Publisher string    `yaml:"publisher"`
}

var CLI *Agent

func init() {
	CLI = &Agent{
		Kind:      "codefly:cli",
		Publisher: "codefly.dev",
		Name:      "cli",
		Version:   shared.Must(Version()),
	}
}

func RegisterAgent(kind AgentKind, protoKind basev1.Agent_Kind) {
	agentKinds[protoKind] = kind
	agentInputs[kind] = protoKind
}

var agentKinds map[basev1.Agent_Kind]AgentKind
var agentInputs map[AgentKind]basev1.Agent_Kind

func init() {
	agentKinds = map[basev1.Agent_Kind]AgentKind{}
	agentInputs = map[AgentKind]basev1.Agent_Kind{}
}

func (p *Agent) String() string {
	return fmt.Sprintf("%s/%s:%s", p.Publisher, p.Name, p.Version)
}

func LoadAgent(ctx context.Context, action *basev1.Agent) (*Agent, error) {
	logger := shared.GetLogger(ctx).With("LoadAgent")
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

func AgentKindFromProto(kind basev1.Agent_Kind) (*AgentKind, error) {
	s, ok := agentKinds[kind]
	if !ok {
		return nil, fmt.Errorf("unknown agent kind: %s", kind)
	}
	return &s, nil
}

func agentKindFromProto(kind AgentKind) (basev1.Agent_Kind, error) {
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

func (p *Agent) Identifier() string {
	return fmt.Sprintf("%s/%s:%s", p.Publisher, p.Name, p.Version)
}

func (p *Agent) Key(kind AgentKind, unique string) string {
	return fmt.Sprintf("%s::%s::%s", kind, p.Identifier(), unique)
}

func (p *Agent) Unique() string {
	return fmt.Sprintf("%s::%s", p.Kind, p.Identifier())
}

func (p *Agent) Of(kind AgentKind) *Agent {
	out := Agent{
		Kind:      kind,
		Publisher: p.Publisher,
		Name:      p.Name,
		Version:   p.Version,
	}
	return &out
}

func (p *Agent) IsService() bool {
	return slices.Contains([]AgentKind{ServiceAgent, FactoryServiceAgent, RuntimeServiceAgent}, p.Kind)
}

func (p *Agent) Path() (string, error) {
	var subdir string
	if p.IsService() {
		subdir = "services"
	} else {
		return "", fmt.Errorf("unknown agent kind: %s", p.Kind)
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

func ParseAgent(ctx context.Context, k AgentKind, s string) (*basev1.Agent, error) {
	logger := shared.GetLogger(ctx).With("ParseAgent<%s>", s)
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
	kind, err := agentKindFromProto(k)
	if err != nil {
		return nil, err
	}

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
