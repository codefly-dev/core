package environment

import (
	"context"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/actions/actions"
	actionsv1 "github.com/codefly-dev/core/generated/go/actions/v1"

	"github.com/codefly-dev/core/configurations"
)

const AddEnvironment = "environment.add"

type AddEnvironmentAction struct {
	*actionsv1.AddEnvironment
}

func (action *AddEnvironmentAction) Command() string {
	return "TODO"
}

func NewActionAddEnvironment(ctx context.Context, in *actionsv1.AddEnvironment) (*AddEnvironmentAction, error) {
	w := wool.Get(ctx).In("NewActionAddEnvironment", wool.Field("name", in.Name))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = AddEnvironment
	return &AddEnvironmentAction{
		AddEnvironment: in,
	}, nil
}

var _ actions.Action = (*AddEnvironmentAction)(nil)

func (action *AddEnvironmentAction) Run(ctx context.Context) (any, error) {
	w := wool.Get(ctx).In("AddEnvironmentAction.Run", wool.Field("name", action.Name))
	// Get project
	ws, err := configurations.LoadWorkspace(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	project, err := ws.LoadProjectFromName(ctx, action.Project)
	if err != nil {
		return nil, w.Wrap(err)
	}

	_, err = project.NewEnvironment(ctx, action.AddEnvironment)
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = project.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save")
	}
	return w, nil
}

func init() {
	actions.RegisterFactory(AddEnvironment, actions.Wrap[*AddEnvironmentAction]())
}
