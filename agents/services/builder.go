package services

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/agents/manager"

	"github.com/codefly-dev/core/agents/communicate"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/configurations"
	agentv0 "github.com/codefly-dev/core/generated/go/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/services/builder/v0"
	"github.com/hashicorp/go-plugin"
	"google.golang.org/grpc"
)

type ServiceBuilderAgentContext struct {
}

func (m ServiceBuilderAgentContext) Key(p *configurations.Agent, unique string) string {
	return p.Key(configurations.BuilderServiceAgent, unique)
}

func (m ServiceBuilderAgentContext) Default() plugin.Plugin {
	return &BuilderAgentGRPC{}
}

var _ manager.AgentContext = ServiceBuilderAgentContext{}

type Builder interface {
	Load(ctx context.Context, req *builderv0.LoadRequest) (*builderv0.LoadResponse, error)
	Init(ctx context.Context, req *builderv0.InitRequest) (*builderv0.InitResponse, error)

	Create(ctx context.Context, req *builderv0.CreateRequest) (*builderv0.CreateResponse, error)
	Update(ctx context.Context, req *builderv0.UpdateRequest) (*builderv0.UpdateResponse, error)

	Sync(ctx context.Context, req *builderv0.SyncRequest) (*builderv0.SyncResponse, error)

	Build(ctx context.Context, req *builderv0.BuildRequest) (*builderv0.BuildResponse, error)
	Deploy(ctx context.Context, req *builderv0.DeploymentRequest) (*builderv0.DeploymentResponse, error)

	// Communicate is a special method that is used to communicate with the agent
	communicate.Communicate
}

type BuilderAgent struct {
	client  builderv0.BuilderClient
	agent   *configurations.Agent
	process *manager.ProcessInfo
}

func (m BuilderAgent) Load(ctx context.Context, req *builderv0.LoadRequest) (*builderv0.LoadResponse, error) {
	return m.client.Load(ctx, req)
}

func (m BuilderAgent) Init(ctx context.Context, req *builderv0.InitRequest) (*builderv0.InitResponse, error) {
	return m.client.Init(ctx, req)
}

func (m BuilderAgent) Create(ctx context.Context, req *builderv0.CreateRequest) (*builderv0.CreateResponse, error) {
	return m.client.Create(ctx, req)
}

func (m BuilderAgent) Update(ctx context.Context, req *builderv0.UpdateRequest) (*builderv0.UpdateResponse, error) {
	return m.client.Update(ctx, req)
}

func (m BuilderAgent) Sync(ctx context.Context, req *builderv0.SyncRequest) (*builderv0.SyncResponse, error) {
	return m.client.Sync(ctx, req)
}

func (m BuilderAgent) Build(ctx context.Context, req *builderv0.BuildRequest) (*builderv0.BuildResponse, error) {
	return m.client.Build(ctx, req)
}

func (m BuilderAgent) Deploy(ctx context.Context, req *builderv0.DeploymentRequest) (*builderv0.DeploymentResponse, error) {
	return m.client.Deploy(ctx, req)
}

func (m BuilderAgent) Communicate(ctx context.Context, req *agentv0.Engage) (*agentv0.InformationRequest, error) {
	return m.client.Communicate(ctx, req)
}

type BuilderAgentGRPC struct {
	// GRPCAgent must still implement the ServiceAgent interface
	plugin.Plugin
	Builder Builder
}

func (p *BuilderAgentGRPC) GRPCServer(_ *plugin.GRPCBroker, s *grpc.Server) error {
	builderv0.RegisterBuilderServer(s, &BuilderServer{Builder: p.Builder})
	return nil
}

func (p *BuilderAgentGRPC) GRPCClient(_ context.Context, _ *plugin.GRPCBroker, c *grpc.ClientConn) (interface{}, error) {
	return &BuilderAgent{client: builderv0.NewBuilderClient(c)}, nil
}

// BuilderServer wraps the gRPC protocol Request/Response
type BuilderServer struct {
	builderv0.UnimplementedBuilderServer
	Builder Builder
}

func (m *BuilderServer) Load(ctx context.Context, req *builderv0.LoadRequest) (*builderv0.LoadResponse, error) {
	return m.Builder.Load(ctx, req)
}

func (m *BuilderServer) Init(ctx context.Context, req *builderv0.InitRequest) (*builderv0.InitResponse, error) {
	return m.Builder.Init(ctx, req)
}

func (m *BuilderServer) Create(ctx context.Context, req *builderv0.CreateRequest) (*builderv0.CreateResponse, error) {
	return m.Builder.Create(ctx, req)
}

func (m *BuilderServer) Update(ctx context.Context, req *builderv0.UpdateRequest) (*builderv0.UpdateResponse, error) {
	return m.Builder.Update(ctx, req)
}

func (m *BuilderServer) Sync(ctx context.Context, req *builderv0.SyncRequest) (*builderv0.SyncResponse, error) {
	return m.Builder.Sync(ctx, req)
}

func (m *BuilderServer) Build(ctx context.Context, req *builderv0.BuildRequest) (*builderv0.BuildResponse, error) {
	return m.Builder.Build(ctx, req)
}

func (m *BuilderServer) Deploy(ctx context.Context, req *builderv0.DeploymentRequest) (*builderv0.DeploymentResponse, error) {
	return m.Builder.Deploy(ctx, req)
}

func (m *BuilderServer) Communicate(ctx context.Context, req *agentv0.Engage) (*agentv0.InformationRequest, error) {
	return m.Builder.Communicate(ctx, req)
}

var buildersCache map[string]int

func init() {
	buildersCache = make(map[string]int)
}

func LoadBuilder(ctx context.Context, conf *configurations.Service) (*BuilderAgent, error) {
	w := wool.Get(ctx).In("services.LoadBuilder", wool.ThisField(conf))
	if buildersCache[conf.Unique()] > 0 {
		return nil, fmt.Errorf("already loaded")
	}
	buildersCache[conf.Unique()]++

	if conf == nil {
		return nil, fmt.Errorf("conf cannot be nil")
	}
	if conf.Agent == nil {
		return nil, w.NewError("agent cannot be nil")
	}
	builder, process, err := manager.Load[ServiceBuilderAgentContext, BuilderAgent](ctx, conf.Agent.Of(configurations.BuilderServiceAgent), conf.Unique())
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service builder conf")
	}
	builder.agent = conf.Agent
	builder.process = process
	return builder, nil
}

func NewBuilderAgent(conf *configurations.Agent, builder Builder) agents.AgentImplementation {
	return agents.AgentImplementation{
		Configuration: conf,
		Agent:         &BuilderAgentGRPC{Builder: builder},
	}
}
