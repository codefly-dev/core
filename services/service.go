package services

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/codefly-dev/core/agents/manager"
	"github.com/codefly-dev/core/agents/services"
	"github.com/codefly-dev/core/failures"
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

	Agent *services.ServiceAgent
	Info  *agentv0.AgentInformation

	Builder *BuilderInstance
	Runtime *RuntimeInstance

	ProcessInfo
	Capabilities []*agentv0.Capability
}

type BuilderInstance struct {
	*Instance

	Builder *services.BuilderAgent
}

type RuntimeInstance struct {
	*Instance

	Runtime *services.RuntimeAgent

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
	w.Trace("loading", wool.ModuleField(instance.Module.Name))
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
	resp, err := instance.Builder.Load(ctx, req)
	if err != nil {
		return resp, err
	}
	if resp != nil && resp.State != nil && resp.State.State == builderv0.LoadStatus_ERROR {
		return resp, operationStatusFailure("builder load", resp.State.Message, resp.State.Failure)
	}
	return resp, nil
}

func (instance *BuilderInstance) Create(ctx context.Context, req *builderv0.CreateRequest, handler communicate.AnswerProvider) (*builderv0.CreateResponse, error) {
	w := wool.Get(ctx).In("BuilderInstance::Create", wool.NameField(instance.Identity.Unique()))

	// Run interactive Q&A via bidirectional streaming
	stream, err := instance.Builder.Communicate(ctx)
	if err != nil {
		return &builderv0.CreateResponse{State: &builderv0.CreateStatus{State: builderv0.CreateStatus_ERROR, Message: err.Error(), Failure: failures.FromError("builder.create", err)}},
			w.Wrapf(err, "cannot open communicate stream")
	}
	err = communicate.Do(ctx, stream, handler)
	if err != nil {
		return &builderv0.CreateResponse{State: &builderv0.CreateStatus{State: builderv0.CreateStatus_ERROR, Message: err.Error(), Failure: failures.FromError("builder.create", err)}},
			w.Wrapf(err, "communicate failed")
	}

	resp, err := instance.Builder.Create(ctx, req)
	if err != nil {
		return resp, err
	}
	if resp != nil && resp.State != nil && resp.State.State == builderv0.CreateStatus_ERROR {
		return resp, operationStatusFailure("builder create", resp.State.Message, resp.State.Failure)
	}
	return resp, nil
}

func (instance *BuilderInstance) Sync(ctx context.Context, req *builderv0.SyncRequest, handler communicate.AnswerProvider) (*builderv0.SyncResponse, error) {
	w := wool.Get(ctx).In("BuilderInstance::Sync", wool.NameField(instance.Identity.Unique()))

	// Run interactive Q&A via bidirectional streaming
	stream, err := instance.Builder.Communicate(ctx)
	if err != nil {
		return &builderv0.SyncResponse{State: &builderv0.SyncStatus{State: builderv0.SyncStatus_ERROR, Message: err.Error(), Failure: failures.FromError("builder.sync", err)}},
			w.Wrapf(err, "cannot open communicate stream")
	}
	err = communicate.Do(ctx, stream, handler)
	if err != nil {
		return &builderv0.SyncResponse{State: &builderv0.SyncStatus{State: builderv0.SyncStatus_ERROR, Message: err.Error(), Failure: failures.FromError("builder.sync", err)}},
			w.Wrapf(err, "communicate failed")
	}

	resp, err := instance.Builder.Sync(ctx, req)
	if err != nil {
		return resp, err
	}
	if resp != nil && resp.State != nil {
		switch resp.State.State {
		case builderv0.SyncStatus_ERROR:
			return resp, operationStatusFailure("builder sync", resp.State.Message, resp.State.Failure)
		case builderv0.SyncStatus_UNSUPPORTED:
			return resp, operationStatusFailure("builder sync unsupported", resp.State.Message, resp.State.Failure)
		}
	}
	return resp, nil
}

// Delegations to the gRPC BuilderClient

func (instance *BuilderInstance) Init(ctx context.Context, req *builderv0.InitRequest) (*builderv0.InitResponse, error) {
	resp, err := instance.Builder.Init(ctx, req)
	if err == nil && resp != nil && resp.State != nil && resp.State.State == builderv0.InitStatus_ERROR {
		err = operationStatusFailure("builder init", resp.State.Message, resp.State.Failure)
	}
	return resp, err
}

func (instance *BuilderInstance) Build(ctx context.Context, req *builderv0.BuildRequest) (*builderv0.BuildResponse, error) {
	resp, err := instance.Builder.Build(ctx, req)
	if err == nil && resp != nil && resp.State != nil && resp.State.State == builderv0.BuildStatus_ERROR {
		err = operationStatusFailure("builder build", resp.State.Message, resp.State.Failure)
	}
	return resp, err
}

func (instance *BuilderInstance) Deploy(ctx context.Context, req *builderv0.DeploymentRequest) (*builderv0.DeploymentResponse, error) {
	resp, err := instance.Builder.Deploy(ctx, req)
	if err == nil && resp != nil && resp.State != nil && resp.State.State == builderv0.DeploymentStatus_ERROR {
		err = operationStatusFailure("builder deploy", resp.State.Message, resp.State.Failure)
	}
	return resp, err
}

func (instance *BuilderInstance) Audit(ctx context.Context, req *builderv0.AuditRequest) (*builderv0.AuditResponse, error) {
	resp, err := instance.Builder.Audit(ctx, req)
	if err == nil && resp != nil && resp.State != nil {
		switch resp.State.State {
		case builderv0.AuditStatus_ERROR:
			err = operationStatusFailure("builder audit", resp.State.Message, resp.State.Failure)
		case builderv0.AuditStatus_UNSUPPORTED:
			err = operationStatusFailure("builder audit unsupported", resp.State.Message, resp.State.Failure)
		}
	}
	return resp, err
}

func (instance *BuilderInstance) SBOM(ctx context.Context, req *builderv0.SBOMRequest) (*builderv0.SBOMResponse, error) {
	resp, err := instance.Builder.SBOM(ctx, req)
	if err == nil && resp != nil && resp.State != nil {
		switch resp.State.State {
		case builderv0.SBOMStatus_ERROR:
			err = operationStatusFailure("builder SBOM", resp.State.Message, resp.State.Failure)
		case builderv0.SBOMStatus_UNSUPPORTED:
			err = operationStatusFailure("builder SBOM unsupported", resp.State.Message, resp.State.Failure)
		}
	}
	return resp, err
}

func (instance *BuilderInstance) Package(ctx context.Context, req *builderv0.PackageRequest) (*builderv0.PackageResponse, error) {
	resp, err := instance.Builder.Package(ctx, req)
	if err == nil && resp != nil && resp.State != nil {
		switch resp.State.State {
		case builderv0.PackageStatus_ERROR:
			err = operationStatusFailure("builder package", resp.State.Message, resp.State.Failure)
		case builderv0.PackageStatus_UNSUPPORTED:
			err = operationStatusFailure("builder package unsupported", resp.State.Message, resp.State.Failure)
		}
	}
	return resp, err
}

func (instance *BuilderInstance) Upgrade(ctx context.Context, req *builderv0.UpgradeRequest) (*builderv0.UpgradeResponse, error) {
	resp, err := instance.Builder.Upgrade(ctx, req)
	if err == nil && resp != nil && resp.State != nil && resp.State.State == builderv0.UpgradeStatus_ERROR {
		err = operationStatusFailure("builder upgrade", resp.State.Message, resp.State.Failure)
	}
	return resp, err
}

func (instance *BuilderInstance) Update(ctx context.Context, req *builderv0.UpdateRequest) (*builderv0.UpdateResponse, error) {
	resp, err := instance.Builder.Update(ctx, req)
	if err == nil && resp != nil && resp.State != nil && resp.State.State == builderv0.UpdateStatus_ERROR {
		err = operationStatusFailure("builder update", resp.State.Message, resp.State.Failure)
	}
	return resp, err
}

// Runtime methods

func (instance *RuntimeInstance) Load(ctx context.Context, env *basev0.Environment) (*runtimev0.LoadResponse, error) {
	w := wool.Get(ctx).In("RuntimeInstance::Load", wool.NameField(instance.Identity.Unique()))
	w.Trace("sending load request")
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
	resp, err := instance.Runtime.Load(ctx, req)
	if err == nil && resp != nil && resp.Status != nil && resp.Status.State == runtimev0.LoadStatus_ERROR {
		err = operationStatusFailure("runtime load", resp.Status.Message, resp.Status.Failure)
	}
	return resp, err
}

// Delegations to the gRPC RuntimeClient

func (instance *RuntimeInstance) Init(ctx context.Context, req *runtimev0.InitRequest) (*runtimev0.InitResponse, error) {
	resp, err := instance.Runtime.Init(ctx, req)
	if err == nil && resp != nil && resp.Status != nil && resp.Status.State == runtimev0.InitStatus_ERROR {
		err = operationStatusFailure("runtime init", resp.Status.Message, resp.Status.Failure)
	}
	return resp, err
}

func (instance *RuntimeInstance) Start(ctx context.Context, req *runtimev0.StartRequest) (*runtimev0.StartResponse, error) {
	resp, err := instance.Runtime.Start(ctx, req)
	if err == nil && resp != nil && resp.Status != nil && resp.Status.State == runtimev0.StartStatus_ERROR {
		err = operationStatusFailure("runtime start", resp.Status.Message, resp.Status.Failure)
	}
	return resp, err
}

func (instance *RuntimeInstance) Stop(ctx context.Context, req *runtimev0.StopRequest) (*runtimev0.StopResponse, error) {
	resp, err := instance.Runtime.Stop(ctx, req)
	if err == nil && resp != nil && resp.Status != nil && resp.Status.State == runtimev0.StopStatus_ERROR {
		err = operationStatusFailure("runtime stop", resp.Status.Message, resp.Status.Failure)
	}
	return resp, err
}

func (instance *RuntimeInstance) Test(ctx context.Context, req *runtimev0.TestRequest) (*runtimev0.TestResponse, error) {
	// Test failures carry structured suites/counts that callers render. Preserve
	// the response-level status here instead of collapsing it into a Go error.
	return instance.Runtime.Test(ctx, req)
}

func (instance *RuntimeInstance) Destroy(ctx context.Context, req *runtimev0.DestroyRequest) (*runtimev0.DestroyResponse, error) {
	resp, err := instance.Runtime.Destroy(ctx, req)
	if err == nil && resp != nil && resp.Status != nil && resp.Status.State == runtimev0.DestroyStatus_ERROR {
		err = operationStatusFailure("runtime destroy", resp.Status.Message, resp.Status.Failure)
	}
	return resp, err
}

func (instance *RuntimeInstance) Information(ctx context.Context, req *runtimev0.InformationRequest) (*runtimev0.InformationResponse, error) {
	return instance.Runtime.Information(ctx, req)
}

func operationStatusError(operation, message string) error {
	return operationStatusFailure(operation, message, nil)
}

func operationStatusFailure(operation, message string, failure *basev0.Failure) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "agent returned an error status"
	}
	presentation := fmt.Sprintf("%s failed: %s", operation, message)
	if failure == nil {
		return fmt.Errorf("%s", presentation)
	}
	return failures.FromFailure(failure, presentation, nil)
}

// Loader

// Instance Cache. Guarded by instancesMu: the CLI fan-loads agents in parallel
// via Flow, so concurrent map read+write here used to panic with "concurrent
// map read and map write" (the sibling cache in agent.go was given connCacheMu
// for the same reason; this one was missed).
var (
	instances   = map[string]*Instance{}
	instancesMu sync.Mutex
)

// Load spawns a single agent process (or reuses a cached one) and creates
// all gRPC clients from the shared connection.
func Load(ctx context.Context, workspace *resources.Workspace, module *resources.Module, service *resources.Service) (*Instance, error) {
	if service == nil {
		return nil, wool.Get(ctx).In("services.Load").NewError("service cannot be nil")
	}
	w := wool.Get(ctx).In("services.Load", wool.NameField(service.Name))
	identity, err := service.Identity()
	if err != nil {
		return nil, w.Wrapf(err, "cannot get service identity")
	}

	instancesMu.Lock()
	cached, ok := instances[identity.Unique()]
	instancesMu.Unlock()
	if ok {
		return cached, nil
	}

	// Load agent -- spawns the binary and connects via gRPC. Key by the SERVICE
	// (identity.Unique()), NOT the agent: two services sharing an agent must get
	// separate processes so the agent's per-service Runtime state can't leak.
	agent, err := LoadAgent(ctx, service.Agent, identity.Unique())
	if err != nil {
		return nil, w.Wrapf(err, "cannot load agent: %s", service.Agent)
	}

	instance := &Instance{
		Workspace: workspace,
		Service:   service,
		Module:    module,
		Identity:  identity,
		Agent:     agent,
	}
	instance.ProcessInfo.AgentPID = agent.ProcessInfo.PID

	info, err := agent.GetAgentInformation(ctx, &agentv0.AgentInformationRequest{})
	if err != nil {
		return nil, w.Wrapf(err, "cannot get agent information: %v", service.Agent)
	}

	instance.Capabilities = info.Capabilities
	instance.Info = info

	// Double-check under lock: another goroutine may have loaded the same
	// identity while we were spawning. Prefer the cached one so callers share
	// a single agent process per identity.
	instancesMu.Lock()
	if existing, found := instances[instance.Identity.Unique()]; found {
		instancesMu.Unlock()
		return existing, nil
	}
	instances[instance.Identity.Unique()] = instance
	instancesMu.Unlock()

	w.Trace("loaded agent", wool.Field("agent-pid", instance.ProcessInfo.AgentPID))
	return instance, nil
}

// LoadBuilder creates a BuilderAgent from the shared connection.
func (instance *Instance) LoadBuilder(ctx context.Context) error {
	w := wool.Get(ctx).In("ServiceInstance::LoadBuilder", wool.NameField(instance.Identity.Unique()))
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

// LoadRuntime creates a RuntimeAgent from the shared connection.
func (instance *Instance) LoadRuntime(ctx context.Context, withRuntimeCheck bool) error {
	w := wool.Get(ctx).In("ServiceInstance::LoadRuntime", wool.NameField(instance.Identity.Unique()))
	err := instance.CheckCapabilities(agentv0.Capability_RUNTIME)
	if err != nil {
		return w.Wrapf(err, "missing runtime capability")
	}
	if withRuntimeCheck {
		err = runners.CheckToolchains(ctx, instance.Info.Toolchains)
		if err != nil {
			return w.Wrapf(err, "missing some toolchains")
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
	_, err := manager.PinToLatestRelease(ctx, service.Agent)
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
