package project

import (
	"context"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/actions/actions"
	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"

	"github.com/codefly-dev/core/configurations"
)

const AddProjectToWorkspaceKind = "project.add_to_workspace"

type AddProjectToWorkspace = actionsv0.AddProjectToWorkspace
type AddProjectToWorkspaceAction struct {
	*AddProjectToWorkspace
}

func (action *AddProjectToWorkspaceAction) Command() string {
	return "codefly switch project"
}

func NewActionAddProjectToWorkspace(ctx context.Context, in *AddProjectToWorkspace) (*AddProjectToWorkspaceAction, error) {
	w := wool.Get(ctx).In("NewActionAddProjectToWorkspace", wool.NameField(in.Name))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = AddProjectToWorkspaceKind
	return &AddProjectToWorkspaceAction{
		AddProjectToWorkspace: in,
	}, nil
}

var _ actions.Action = (*AddProjectToWorkspaceAction)(nil)

func (action *AddProjectToWorkspaceAction) Run(ctx context.Context) (any, error) {
	w := wool.Get(ctx).In("AddProjectToWorkspaceAction.Run", wool.NameField(action.Name))
	workspace, err := configurations.LoadWorkspace(ctx, action.Workspace)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load workspace")
	}

	err = workspace.AddProjectReference(ctx, &configurations.ProjectReference{
		Name: action.Name,
		Path: action.Path,
	})
	if err != nil {
		return nil, w.Wrapf(err, "cannot add project reference")
	}
	err = workspace.SetProjectActive(ctx, action.Name)
	if err != nil {
		return nil, w.Wrapf(err, "cannot set project active")
	}

	return workspace, nil
}

func init() {
	actions.RegisterBuilder(AddProjectToWorkspaceKind, actions.Wrap[*AddProjectToWorkspaceAction]())
}
