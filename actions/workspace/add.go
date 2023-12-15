package workspace

import (
	"context"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/shared"

	actionsv1 "github.com/codefly-dev/core/generated/go/actions/v1"

	"github.com/codefly-dev/core/configurations"
)

const AddWorkspace = "workspace.add"

type AddWorkspaceAction struct {
	*actionsv1.AddWorkspace
}

func (action *AddWorkspaceAction) Command() string {
	return "TODO"
}

func NewActionAddWorkspace(ctx context.Context, in *actionsv1.AddWorkspace) (*AddWorkspaceAction, error) {
	logger := shared.GetLogger(ctx).With(shared.ProtoType(in))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, logger.Wrap(err)
	}
	in.Kind = AddWorkspace
	return &AddWorkspaceAction{
		AddWorkspace: in,
	}, nil
}

var _ actions.Action = (*AddWorkspaceAction)(nil)

func (action *AddWorkspaceAction) Run(ctx context.Context) (any, error) {
	logger := shared.GetLogger(ctx).With("AddWorkspaceAction")
	w, err := configurations.NewWorkspace(ctx, action.AddWorkspace)
	if err != nil {
		return nil, logger.Wrap(err)
	}
	err = w.Save(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save")
	}
	return w, nil
}

func init() {
	actions.RegisterFactory(AddWorkspace, actions.Wrap[*AddWorkspaceAction]())
}
