package services

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/agents/manager"
	"github.com/codefly-dev/core/agents/services"
	runners "github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/agents/communicate"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
)

type ProcessInfo struct {
	AgentPID int
}

type Instance struct {
	Workspace *resources.Workspace
	Module    *resources.Module
	Service   *resources.Service

	Identity *resources.ServiceIdentity

	Agent services.Agent
	Info  *agentv0.AgentInformation

	Builder *BuilderInstance
	Runtime *RuntimeInstance

	ProcessInfo
	Capabilities []*agentv0.Capability
}

type BuilderInstance struct {
	*Instance

	services.Builder
}

type RuntimeInstance struct {
	*Instance

	services.Runtime

	IsHotReloading bool
}

// Builder methods

func (instance *BuilderInstance) loadRequest(ctx context.Context) (*builderv0.LoadRequest, error) {
	w := wool.Get(ctx).In("BuilderInstance::loadRequest", wool.NameField(instance.Identity.Unique()))
	relativeToWorkspace, err := instance.Workspace.RelativeDir(instance.Service)
	if err != nil {
		return nil, w.Wrapf(err, "cannot compute relative dir")

	}
	req := &builderv0.LoadRequest{
		Identity: &basev0.ServiceIdentity{
			Name:                instance.Service.Name,
			Module:              instance.Module.Name,
			Version:             instance.Service.Version,
			Workspace:           instance.Workspace.Name,
			WorkspacePath:       instance.Workspace.Dir(),
			RelativeToWorkspace: relativeToWorkspace,
		},
	}
	return req, nil
}

type BuilderLoadOptions struct {
	create bool
	sync   bool
}

type BuilderLoadOption func(opt *BuilderLoadOptions)

func ForCreate(opt *BuilderLoadOptions) {
	opt.create = true
}

func ForSync(opt *BuilderLoadOptions) {
	opt.sync = true
}

func (instance *BuilderInstance) Load(ctx context.Context, opts ...BuilderLoadOption) (*builderv0.LoadResponse, error) {
	opt := &BuilderLoadOptions{}
	for _, o := range opts {
		o(opt)
	}
	w := wool.Get(ctx).In("BuilderInstance::Load", wool.NameField(instance.Identity.Unique()))
	w.Debug("loading", wool.ModuleField(instance.Module.Name))
	req, err := instance.loadRequest(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create load request")
	}
	if opt.create {
		req.CreationMode = &builderv0.CreationMode{Communicate: true}
	}
	if opt.sync {
		req.SyncMode = &builderv0.SyncMode{Communicate: true}
	}
	return instance.Builder.Load(ctx, req)
}

func (instance *BuilderInstance) Create(ctx context.Context, req *builderv0.CreateRequest, handler communicate.AnswerProvider) (*builderv0.CreateResponse, error) {
	w := wool.Get(ctx).In("BuilderInstance::Create", wool.NameField(instance.Identity.Unique()))
	err := communicate.Do[builderv0.CreateRequest](ctx, instance.Builder, handler)
	if err != nil {
		return &builderv0.CreateResponse{State: &builderv0.CreateStatus{State: builderv0.CreateStatus_ERROR, Message: err.Error()}},
			w.Wrapf(err, "cannot communicate")
	}
	return instance.Builder.Create(ctx, req)
}

func (instance *BuilderInstance) Sync(ctx context.Context, req *builderv0.SyncRequest, handler communicate.AnswerProvider) (*builderv0.SyncResponse, error) {
	w := wool.Get(ctx).In("BuilderInstance::Sync", wool.NameField(instance.Identity.Unique()))
	// Communicate always
	err := communicate.Do[builderv0.SyncRequest](ctx, instance.Builder, handler)
	if err != nil {
		return &builderv0.SyncResponse{State: &builderv0.SyncStatus{State: builderv0.SyncStatus_ERROR, Message: err.Error()}},
			w.Wrapf(err, "cannot communicate")
	}
	return instance.Builder.Sync(ctx, req)
}

// Runner methods

func (instance *RuntimeInstance) Load(ctx context.Context, env *basev0.Environment) (*runtimev0.LoadResponse, error) {
	w := wool.Get(ctx).In("RuntimeInstance::Load", wool.NameField(instance.Identity.Unique()))
	w.Debug("sending load request")
	relativeToWorkspace, err := instance.Workspace.RelativeDir(instance.Service)
	if err != nil {
		return nil, w.Wrapf(err, "cannot compute relative dir")
	}
	req := &runtimev0.LoadRequest{
		DeveloperDebug: wool.IsDebug(),
		Identity: &basev0.ServiceIdentity{
			Name:                instance.Service.Name,
			Version:             instance.Service.Version,
			Module:              instance.Module.Name,
			Workspace:           instance.Workspace.Name,
			WorkspacePath:       instance.Workspace.Dir(),
			RelativeToWorkspace: relativeToWorkspace,
		},
		Environment: env,
	}
	err = resources.Validate(req)
	if err != nil {
		return nil, w.Wrapf(err, "invalid request")
	}
	return instance.Runtime.Load(ctx, req)
}

// Loader

// Instance Cache
var instances = map[string]*Instance{}

func init() {
	instances = make(map[string]*Instance)
}

func Load(ctx context.Context, module *resources.Module, service *resources.Service) (*Instance, error) {
	w := wool.Get(ctx).In("services.Load", wool.NameField(service.Name))
	identity, err := service.Identity()
	if err != nil {
		return nil, w.Wrapf(err, "cannot get service identity")
	}

	if service == nil {
		return nil, w.NewError("service cannot be nil")
	}
	if instance, ok := instances[identity.Unique()]; ok {
		return instance, nil
	}

	agent, err := LoadAgent(ctx, service.Agent)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load agent: %s", service.Agent)
	}
	// Init capabilities
	instance := &Instance{
		Service:  service,
		Module:   module,
		Identity: identity,
		Agent:    agent,
	}
	instance.ProcessInfo.AgentPID = agent.ProcessInfo.PID

	info, err := agent.GetAgentInformation(ctx, &agentv0.AgentInformationRequest{})
	if err != nil {
		return nil, w.Wrapf(err, "cannot get agent information: %v", service.Agent)
	}

	instance.Capabilities = info.Capabilities

	instance.Info = info

	instances[instance.Identity.Unique()] = instance

	w.Debug("loaded agent", wool.Field("agent-pid", instance.ProcessInfo.AgentPID))
	return instance, nil
}

func (instance *Instance) LoadBuilder(ctx context.Context) error {
	w := wool.Get(ctx).In("ServiceInstance::LoadBuilder", wool.NameField(instance.Identity.Unique()))
	if builder, ok := buildersCache[instance.Identity.Unique()]; ok {
		instance.Builder = &BuilderInstance{Instance: instance, Builder: builder}
		return nil
	}
	err := instance.CheckCapabilities(agentv0.Capability_BUILDER)
	if err != nil {
		return w.Wrapf(err, "missing builder capability")
	}
	builder, err := LoadBuilder(ctx, instance.Service)
	if err != nil {
		return w.Wrapf(err, "cannot load builder")
	}
	instance.Builder = &BuilderInstance{Instance: instance, Builder: builder}
	return nil
}

func (instance *Instance) LoadRuntime(ctx context.Context, withRuntimeCheck bool) error {
	w := wool.Get(ctx).In("ServiceInstance::LoadRuntime", wool.NameField(instance.Identity.Unique()))
	if runtime, ok := runtimesCache[instance.Identity.Unique()]; ok {
		instance.Runtime = &RuntimeInstance{Instance: instance, Runtime: runtime}
		return nil
	}
	err := instance.CheckCapabilities(agentv0.Capability_RUNTIME)
	if err != nil {
		return w.Wrapf(err, "missing builder capability")
	}
	if withRuntimeCheck {
		err = runners.CheckForRuntimes(ctx, instance.Info.RuntimeRequirements)
		if err != nil {
			return w.Wrapf(err, "missing some runtimes")
		}
	}
	runtime, err := LoadRuntime(ctx, instance.Service)
	if err != nil {
		return w.Wrapf(err, "cannot load runtime")
	}
	// native hot-reload
	var hotReload bool
	for _, c := range instance.Capabilities {
		if c.Type == agentv0.Capability_HOT_RELOAD {
			hotReload = true
			break
		}
	}
	instance.Runtime = &RuntimeInstance{Instance: instance, Runtime: runtime, IsHotReloading: hotReload}
	return nil
}

func (instance *Instance) CheckCapabilities(capability agentv0.Capability_Type) error {
	for _, c := range instance.Capabilities {
		if c.Type == capability {
			return nil
		}
	}
	return fmt.Errorf("missing capability %v", capability)
}

func (instance *Instance) WithWorkspace(workspace *resources.Workspace) {
	instance.Workspace = workspace
}

func (instance *Instance) Unique() string {
	return instance.Identity.Unique()
}

type AgentUpdate struct {
	Name string
	From string
	To   string
}

type UpdateInformation struct {
	*AgentUpdate
}

func UpdateAgent(ctx context.Context, service *resources.Service) (*UpdateInformation, error) {
	w := wool.Get(ctx).In("ServiceInstance::Update")
	agentVersion := service.Agent.Version
	info := &UpdateInformation{}
	// Fetch the latest agent version
	err := manager.PinToLatestRelease(ctx, service.Agent)
	if err != nil {
		return nil, w.Wrap(err)
	}
	if service.Agent.Version != agentVersion {
		info.AgentUpdate = &AgentUpdate{Name: service.Agent.Name, From: agentVersion, To: service.Agent.Version}
	}
	err = service.Save(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	return info, nil
}
