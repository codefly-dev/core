package workspace

import (
	"context"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/actions/actions"
	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"

	"github.com/codefly-dev/core/configurations"
)

const AddWorkspace = "workspace.add"

type AddWorkspaceAction struct {
	*actionsv0.AddWorkspace
}

func (action *AddWorkspaceAction) Command() string {
	return "TODO"
}

func NewActionAddWorkspace(ctx context.Context, in *actionsv0.AddWorkspace) (*AddWorkspaceAction, error) {
	w := wool.Get(ctx).In("workspace.NewActionAddWorkspace")
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = AddWorkspace
	return &AddWorkspaceAction{
		AddWorkspace: in,
	}, nil
}

var _ actions.Action = (*AddWorkspaceAction)(nil)

func (action *AddWorkspaceAction) Run(ctx context.Context) (any, error) {
	w := wool.Get(ctx).In("workspace.AddWorkspaceAction.Run")
	workspace, err := configurations.NewWorkspace(ctx, action.AddWorkspace)
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = workspace.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save")
	}
	return workspace, nil
}

func init() {
	actions.RegisterFactory(AddWorkspace, actions.Wrap[*AddWorkspaceAction]())
}
