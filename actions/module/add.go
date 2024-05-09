package module

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/actions/actions"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
)

type AddModule = actionsv0.NewModule

type AddModuleAction struct {
	*AddModule
}

func (action *AddModuleAction) Command() string {
	return fmt.Sprintf("codefly add module %s", action.Name)
}

func NewActionAddModule(ctx context.Context, in *AddModule) (*AddModuleAction, error) {
	w := wool.Get(ctx).In("NewActionAddModule", wool.NameField(in.Name))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = actions.AddModule
	return &AddModuleAction{
		AddModule: in,
	}, nil
}

var _ actions.Action = (*AddModuleAction)(nil)

func (action *AddModuleAction) Run(ctx context.Context, space *actions.Space) (any, error) {
	w := wool.Get(ctx).In("AddModuleAction.Run", wool.NameField(action.Name))
	if space.Workspace.Layout == resources.LayoutKindFlat {
		return nil, w.NewError("cannot add module to workspace with layout %s", space.Workspace.Layout)
	}
	module, err := space.Workspace.NewModule(ctx, action.AddModule)
	if err != nil {
		return nil, w.Wrap(err)
	}
	return module, nil
}

func init() {
	actions.RegisterBuilder(actions.AddModule, actions.Wrap[*AddModuleAction]())
}
