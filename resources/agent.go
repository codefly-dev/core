package resources

import (
	"context"
	"fmt"
	"slices"

	"github.com/codefly-dev/core/version"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"

	"github.com/bufbuild/protovalidate-go"

	"path"
	"strings"

	basev0 "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/wool"

	"github.com/Masterminds/semver"

	"github.com/codefly-dev/core/shared"
)

const AgentConfigurationName = "agent.codefly.yaml"

type AgentKind string

type Agent struct {
	Kind      AgentKind `yaml:"kind" json:"kind"`
	Name      string    `yaml:"name" json:"name"`
	Version   string    `yaml:"version" json:"version"`
	Publisher string    `yaml:"publisher" json:"publisher"`
}

var CLI *Agent

func init() {
	CLI = &Agent{
		Kind:      "codefly:cli",
		Publisher: "codefly.dev",
		Name:      "cli",
		Version:   shared.Must(version.Version(context.Background())),
	}
}

func RegisterAgent(kind AgentKind, protoKind basev0.Agent_Kind) {
	agentKinds[protoKind] = kind
	agentInputs[kind] = protoKind
}

var agentKinds map[basev0.Agent_Kind]AgentKind
var agentInputs map[AgentKind]basev0.Agent_Kind

func init() {
	agentKinds = map[basev0.Agent_Kind]AgentKind{}
	agentInputs = map[AgentKind]basev0.Agent_Kind{}
}

func (p *Agent) String() string {
	return fmt.Sprintf("%s/%s:%s", p.Publisher, p.Name, p.Version)
}

func LoadAgent(ctx context.Context, action *basev0.Agent) (*Agent, error) {
	w := wool.Get(ctx).In("LoadAgent")
	if err := ValidateAgent(action); err != nil {
		return nil, w.Wrapf(err, "invalid agent")
	}
	p := &Agent{
		Kind:      agentKinds[action.Kind],
		Publisher: action.Publisher,
		Name:      action.Name,
		Version:   action.Version,
	}
	return p, nil
}

func AgentKindFromProto(kind basev0.Agent_Kind) (*AgentKind, error) {
	s, ok := agentKinds[kind]
	if !ok {
		return nil, fmt.Errorf("unknown agent kind: %s", kind)
	}
	return &s, nil
}

func agentKindFromProto(kind AgentKind) (basev0.Agent_Kind, error) {
	k, ok := agentInputs[kind]
	if !ok {
		return basev0.Agent_UNKNOWN, fmt.Errorf("unknown agent kind: %s", kind)
	}
	return k, nil
}

func ValidateAgent(agent *basev0.Agent) error {
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
	return slices.Contains([]AgentKind{ServiceAgent, BuilderServiceAgent, RuntimeServiceAgent}, p.Kind)
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
	return path.Join(CodeflyDir(), "agents", subdir, name), nil
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

func ParseAgent(ctx context.Context, k AgentKind, s string) (*Agent, error) {
	agent, err := parseAgent(ctx, k, s)
	if err != nil {
		return nil, err
	}
	return AgentFromProto(agent), nil
}

func parseAgent(ctx context.Context, k AgentKind, s string) (*basev0.Agent, error) {
	w := wool.Get(ctx).In("parseAgent", wool.Field("kind", k), wool.Field("agent", s))
	// TODO: More validation
	if s == "" {
		return nil, fmt.Errorf("emmpty")
	}
	tokens := strings.SplitN(s, "/", 2)
	pub := "codefly.dev"
	rest := s
	if len(tokens) == 2 {
		pub = tokens[0]
		rest = tokens[1]
	}
	tokens = strings.Split(rest, ":")
	if len(tokens) > 2 || len(tokens) == 0 {
		return nil, fmt.Errorf("invalid agent Name (should be of the form identifier:ver): %s", s)
	}
	identifier := tokens[0]
	ver := "latest"
	if len(tokens) == 1 {
		w.Warn("no version for agent specified, using latest")
	} else {
		ver = tokens[1]
	}
	kind, err := agentKindFromProto(k)
	if err != nil {
		return nil, err
	}

	agent := &basev0.Agent{Kind: kind, Publisher: pub, Name: identifier, Version: ver}
	v, err := protovalidate.New()
	if err != nil {
		return nil, err
	}
	if err = v.Validate(agent); err != nil {
		return nil, err
	}
	return agent, nil
}

func AgentFromProto(agent *basev0.Agent) *Agent {
	return &Agent{
		Kind:      agentKinds[agent.Kind],
		Publisher: agent.Publisher,
		Name:      agent.Name,
		Version:   agent.Version,
	}
}

func (p *Agent) Proto() *basev0.Agent {
	return &basev0.Agent{
		Kind:      agentInputs[p.Kind],
		Publisher: p.Publisher,
		Name:      p.Name,
		Version:   p.Version,
	}
}

func (p *Agent) AsResource() *wool.Resource {
	r := resource.NewSchemaless(toAttributes(p)...)
	return &wool.Resource{
		Identifier: &wool.Identifier{
			Kind:   "agent",
			Unique: p.Identifier(),
		},
		Resource: r}
}

func toAttributes(_ *Agent) []attribute.KeyValue {
	var attr []attribute.KeyValue
	return attr
}
