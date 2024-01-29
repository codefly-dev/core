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
		ctx = shared.WithOverride(ctx, shared.OverrideAll())
	}

	workspace, err := configurations.LoadWorkspace(ctx, action.Workspace)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load worspace")
	}

	project, err := workspace.LoadProjectFromName(ctx, action.Project)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load project")
	}

	application, err := project.LoadApplicationFromName(ctx, action.Application)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load application")
	}

	service, err := application.NewService(ctx, action.AddService)
	if err != nil {
		return nil, w.Wrapf(err, "cannot application.NewService")
	}

	// Reload application
	application, err = configurations.ReloadApplication(ctx, application)
	if err != nil {
		return nil, w.Wrapf(err, "cannot reload application")
	}

	ref, err := service.Reference()
	if err != nil {
		return nil, w.Wrapf(err, "cannot get service reference")
	}
	err = workspace.AddService(ctx, project.Name, application.Name, ref)
	if err != nil {
		return nil, w.Wrapf(err, "cannot add service")
	}

	err = workspace.SetActiveService(ctx, project.Name, application.Name, service.Name)
	if err != nil {
		return nil, w.Wrapf(err, "cannot set active service")
	}

	err = workspace.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save workspace")
	}

	return service, nil
}

func init() {
	actions.RegisterBuilder(AddServiceKind, actions.Wrap[*AddServiceAction]())
}
