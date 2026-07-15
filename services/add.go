package services

import (
	"context"
	"errors"
	"fmt"

	"github.com/codefly-dev/core/agents/communicate"

	"github.com/codefly-dev/core/actions/actions"
	actionservice "github.com/codefly-dev/core/actions/service"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

type AddOutput struct {
	ReadMe string
}

func Add(ctx context.Context, workspace *resources.Workspace, module *resources.Module, input *actionservice.AddService, handler communicate.AnswerProvider) (output *AddOutput, result error) {
	if workspace == nil {
		return nil, fmt.Errorf("workspace is nil")
	}
	if module == nil {
		return nil, fmt.Errorf("module is nil")
	}
	if input == nil {
		return nil, fmt.Errorf("add service input is nil")
	}
	w := wool.Get(ctx).In("services.Add", wool.Field("workspace", workspace.Name), wool.Field("module", module.Name), wool.Field("input", input))
	created := !module.ExistsService(ctx, input.Name)
	var service *resources.Service
	defer func() {
		if result == nil || !created || service == nil {
			return
		}
		cleanupCtx := context.WithoutCancel(ctx)
		if err := module.DeleteService(cleanupCtx, service.Name); err != nil {
			result = errors.Join(result, w.Wrapf(err, "cannot roll back partial service"))
		}
	}()

	action, err := actionservice.NewActionAddService(ctx, input)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create action")
	}

	out, err := actions.Run(ctx, action, &actions.Space{Module: module})
	if err != nil {
		return nil, w.Wrapf(err, "cannot run AddService action")
	}

	service, err = actions.As[resources.Service](out)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get service back from action output")
	}

	service.WithModule(module.Name)

	instance, err := Load(ctx, workspace, module, service)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service instance")
	}

	err = instance.LoadBuilder(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load builder for service instance")
	}

	info, err := instance.Agent.GetAgentInformation(ctx, &agentv0.AgentInformationRequest{})
	if err != nil {
		return nil, w.Wrapf(err, "cannot get agent information")
	}

	output = &AddOutput{
		ReadMe: info.ReadMe,
	}

	_, err = instance.Builder.Load(ctx, ForCreate)
	if err != nil {
		return nil, w.Wrapf(err, "builder failed in load")
	}

	_, err = instance.Builder.Create(ctx, &builderv0.CreateRequest{}, handler)
	if err != nil {
		return nil, w.Wrapf(err, "builder failed in create")

	}
	return output, nil
}
