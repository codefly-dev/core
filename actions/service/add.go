package service

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/actions/actions"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
	"github.com/codefly-dev/core/wool"
)

const AddServiceKind = "service.add"

type AddServiceAction struct {
	*AddService
}

func (action *AddServiceAction) Command() string {
	agent := configurations.AgentFromProto(action.Agent)
	return fmt.Sprintf("codefly add service %s --agent=%s", action.Name, agent.Identifier())
}

type AddService = actionsv0.AddService

func NewActionAddService(ctx context.Context, in *AddService) (*AddServiceAction, error) {
	w := wool.Get(ctx).In("actions.service.NewActionAddService")
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = AddServiceKind
	return &AddServiceAction{
		AddService: in,
	}, nil
}

var _ actions.Action = (*AddServiceAction)(nil)

func (action *AddServiceAction) Run(ctx context.Context) (any, error) {
	w := wool.Get(ctx).In("actions.service.AddServiceAction.Run")
	if action.Override {
		ctx = shared.WithOverride(ctx, shared.SilentOverride())
	}

	ws, err := configurations.LoadWorkspace(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}

	project, err := ws.LoadProjectFromName(ctx, action.Project)
	if err != nil {
		return nil, w.Wrap(err)
	}

	app, err := project.LoadApplicationFromName(ctx, action.Application)
	if err != nil {
		return nil, w.Wrap(err)
	}

	service, err := app.NewService(ctx, action.AddService)
	if err != nil {
		return nil, w.Wrap(err)
	}

	err = app.SetActiveService(ctx, service.Name)
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = app.Save(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}

	return service, nil
}

func init() {
	actions.RegisterFactory(AddServiceKind, actions.Wrap[*AddServiceAction]())
}
