package service

import (
	"context"
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/actions/actions"

	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
)

const AddServiceKind = "service.add"

type AddServiceAction struct {
	*AddService
}

type AddService = v1actions.AddService

func NewActionAddService(in *AddService) *AddServiceAction {
	in.Kind = AddServiceKind
	return &AddServiceAction{
		AddService: in,
	}
}

var _ actions.Action = (*AddServiceAction)(nil)

func (action *AddServiceAction) Run(ctx context.Context) (any, error) {
	logger := shared.GetBaseLogger(ctx).With("AddServiceAction")
	ws, err := configurations.ActiveWorkspace(ctx)
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
	logger.Debugf("creating service %s", service.Name)
	return service, nil
}

func init() {
	actions.RegisterFactory(AddServiceKind, actions.Wrap[*AddServiceAction]())
}
