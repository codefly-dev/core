package service

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/actions/actions"

	actionsv1 "github.com/codefly-dev/core/generated/go/actions/v1"
)

const AddServiceKind = "service.add"

type AddServiceAction struct {
	*AddService
}

func (action *AddServiceAction) Command() string {
	agent := configurations.AgentFromProto(action.Agent)
	return fmt.Sprintf("codefly add service %s --agent=%s", action.Name, agent.Identifier())
}

type AddService = actionsv1.AddService

func NewActionAddService(ctx context.Context, in *AddService) (*AddServiceAction, error) {
	logger := shared.GetLogger(ctx).With(shared.ProtoType(in))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, logger.Wrap(err)
	}
	in.Kind = AddServiceKind
	return &AddServiceAction{
		AddService: in,
	}, nil
}

var _ actions.Action = (*AddServiceAction)(nil)

func (action *AddServiceAction) Run(ctx context.Context) (any, error) {
	logger := shared.GetLogger(ctx).With("AddServiceAction")

	if action.Override {
		ctx = shared.WithOverride(ctx, shared.SilentOverride())
	}

	ws, err := configurations.LoadWorkspace(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot get current workspace")
	}

	project, err := ws.LoadProjectFromName(ctx, action.Project)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load project %s", action.Project)
	}

	app, err := project.LoadApplicationFromName(ctx, action.Application)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load application %s", action.Application)
	}

	service, err := app.NewService(ctx, action.AddService)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot add service %s", action.Name)
	}

	err = app.SetActiveService(ctx, service.Name)
	if err != nil {
		return nil, logger.Wrap(err)
	}

	err = app.Save(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save project")
	}

	return service, nil
}

func init() {
	actions.RegisterFactory(AddServiceKind, actions.Wrap[*AddServiceAction]())
}
