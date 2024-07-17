package services

import (
	"context"

	resources "github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/agents/manager"
	coreservices "github.com/codefly-dev/core/agents/services"

	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
)

/*
Loader
*/

var runtimesCache map[string]*coreservices.RuntimeAgent
var runtimesPid map[string]int

func init() {
	runtimesCache = make(map[string]*coreservices.RuntimeAgent)
	runtimesPid = make(map[string]int)
}

func LoadRuntime(ctx context.Context, service *resources.Service) (*coreservices.RuntimeAgent, error) {
	if service == nil {
		return nil, wool.Get(ctx).NewError("service cannot be nil")
	}
	if service.Agent == nil {
		return nil, wool.Get(ctx).NewError("agent cannot be nil")
	}
	w := wool.Get(ctx).In("services.LoadRuntime", wool.ServiceField(service.Name))

	identity, err := service.Identity()
	if err != nil {
		return nil, w.Wrapf(err, "cannot get service identity")
	}

	if runtime, ok := runtimesCache[identity.Unique()]; ok {
		return runtime, nil
	}

	runtime, process, err := manager.Load[coreservices.ServiceRuntimeAgentContext, coreservices.RuntimeAgent](
		ctx,
		service.Agent.Of(resources.RuntimeServiceAgent),
		identity.Unique())
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service runtime agent")
	}

	runtimesPid[identity.Unique()] = process.PID

	runtime.Agent = service.Agent
	runtime.ProcessInfo = process

	w.Debug("loaded runtime", wool.Field("runtime-pid", process.PID))

	runtimesCache[identity.Unique()] = runtime

	return runtime, nil
}

type InformationStatus struct {
	Load  *runtimev0.LoadStatus
	Init  *runtimev0.InitStatus
	Start *runtimev0.StartStatus

	DesiredState *runtimev0.DesiredState
}
