package services

import (
	"context"

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

func Add(ctx context.Context, workspace *resources.Workspace, module *resources.Module, input *actionservice.AddService, handler communicate.AnswerProvider) (*AddOutput, error) {
	w := wool.Get(ctx).In("services.Add")
	action, err := actionservice.NewActionAddService(ctx, input)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create action")
	}

	out, err := actions.Run(ctx, action, &actions.Space{Module: module})
	if err != nil {
		return nil, w.Wrapf(err, "cannot add service")
	}

	service, err := actions.As[resources.Service](out)
	if err != nil {
		return nil, w.Wrapf(err, "cannot add service")
	}

	instance, err := Load(ctx, service)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service instance")
	}

	instance.WithWorkspace(workspace)

	err = instance.LoadBuilder(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service instance")
	}

	info, err := instance.Agent.GetAgentInformation(ctx, &agentv0.AgentInformationRequest{})
	if err != nil {
		return nil, w.Wrapf(err, "cannot get agent information")
	}

	output := &AddOutput{
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
