package application

import (
	"context"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/actions/actions"
	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"

	"github.com/codefly-dev/core/configurations"
)

const AddApplicationToWorkspaceKind = "application.add_to_workspace"

type AddApplicationToWorkspace = actionsv0.AddApplicationToWorkspace
type AddApplicationToWorkspaceAction struct {
	*AddApplicationToWorkspace
}

func (action *AddApplicationToWorkspaceAction) Command() string {
	return "codefly switch application"
}

func NewActionAddApplicationToWorkspace(ctx context.Context, in *AddApplicationToWorkspace) (*AddApplicationToWorkspaceAction, error) {
	w := wool.Get(ctx).In("NewActionAddApplicationToWorkspace", wool.NameField(in.Name))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = AddApplicationToWorkspaceKind
	return &AddApplicationToWorkspaceAction{
		AddApplicationToWorkspace: in,
	}, nil
}

var _ actions.Action = (*AddApplicationToWorkspaceAction)(nil)

func (action *AddApplicationToWorkspaceAction) Run(ctx context.Context) (any, error) {
	w := wool.Get(ctx).In("AddApplicationToWorkspaceAction.Run", wool.NameField(action.Name))
	workspace, err := configurations.LoadWorkspace(ctx, action.Workspace)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load workspace")
	}

	err = workspace.AddApplicationReference(ctx, action.Project, &configurations.ApplicationReference{
		Name: action.Name,
	})
	if err != nil {
		return nil, w.Wrapf(err, "cannot set application active")
	}

	err = workspace.SetApplicationActive(ctx, action.Project, action.Name)
	if err != nil {
		return nil, w.Wrapf(err, "cannot set application active")
	}
	err = workspace.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save workspace")
	}
	return workspace, nil
}

func init() {
	actions.RegisterBuilder(AddApplicationToWorkspaceKind, actions.Wrap[*AddApplicationToWorkspaceAction]())
}
