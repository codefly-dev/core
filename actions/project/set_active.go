package project

import (
	"context"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/actions/actions"
	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"

	"github.com/codefly-dev/core/configurations"
)

const SetProjectActiveKind = "project.set_as_active"

type SetProjectActive = actionsv0.SetProjectActive
type SetProjectActiveAction struct {
	*SetProjectActive
}

func (action *SetProjectActiveAction) Command() string {
	return "codefly switch project"
}

func NewActionSetProjectActive(ctx context.Context, in *SetProjectActive) (*SetProjectActiveAction, error) {
	w := wool.Get(ctx).In("NewActionSetProjectActive", wool.NameField(in.Name))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = SetProjectActiveKind
	return &SetProjectActiveAction{
		SetProjectActive: in,
	}, nil
}

var _ actions.Action = (*SetProjectActiveAction)(nil)

func (action *SetProjectActiveAction) Run(ctx context.Context) (any, error) {
	w := wool.Get(ctx).In("SetProjectActiveAction.Run", wool.NameField(action.Name))
	workspace, err := configurations.LoadWorkspace(ctx, action.Workspace)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load workspace")
	}

	err = workspace.SetProjectActive(ctx, action.SetProjectActive)
	if err != nil {
		return nil, w.Wrapf(err, "cannot set project active")
	}

	workspace, err = configurations.ReloadWorkspace(ctx, workspace)
	if err != nil {
		return nil, w.Wrapf(err, "cannot reload workspace")
	}

	// Return the project
	project, err := workspace.LoadActiveProject(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load active project")
	}
	return project, nil
}

func init() {
	actions.RegisterBuilder(SetProjectActiveKind, actions.Wrap[*SetProjectActiveAction]())
}
