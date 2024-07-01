package workspace

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/bufbuild/protovalidate-go"

	"github.com/codefly-dev/core/actions/actions"
	actionsv0 "github.com/codefly-dev/core/generated/go/codefly/actions/v0"
)

type NewWorkspace = actionsv0.NewWorkspace
type NewWorkspaceAction struct {
	*NewWorkspace
}

func (action *NewWorkspaceAction) Command() string {
	return fmt.Sprintf("codefly add  %s", action.Name)
}

func NewActionNewWorkspace(ctx context.Context, in *NewWorkspace) (*NewWorkspaceAction, error) {
	w := wool.Get(ctx).In(".NewActionNew")
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = actions.NewWorkspace
	return &NewWorkspaceAction{
		NewWorkspace: in,
	}, nil
}

var _ actions.Action = (*NewWorkspaceAction)(nil)

func (action *NewWorkspaceAction) Run(ctx context.Context, _ *actions.Space) (any, error) {
	w := wool.Get(ctx).In(".NewAction.Run")

	// Validate
	v, err := protovalidate.New()
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = v.Validate(action)
	if err != nil {
		return nil, w.Wrap(err)
	}

	workspace, err := resources.CreateWorkspace(ctx, action.NewWorkspace)
	if err != nil {
		return nil, err
	}

	if err = workspace.Valid(); err != nil {
		return nil, w.Wrap(err)
	}
	return workspace, nil
}

func init() {
	actions.RegisterBuilder(actions.NewWorkspace, actions.Wrap[*NewWorkspaceAction]())
}
