package environment

import (
	"context"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/shared"

	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"

	"github.com/codefly-dev/core/configurations"
)

const AddEnvironment = "environment.add"

type AddEnvironmentAction struct {
	*v1actions.AddEnvironment
}

func NewActionAddEnvironment(in *v1actions.AddEnvironment) *AddEnvironmentAction {
	in.Kind = AddEnvironment
	return &AddEnvironmentAction{
		AddEnvironment: in,
	}
}

var _ actions.Action = (*AddEnvironmentAction)(nil)

func (action *AddEnvironmentAction) Run(ctx context.Context) (any, error) {
	logger := shared.GetBaseLogger(ctx).With("AddEnvironmentAction")
	// Get project
	ws, err := configurations.ActiveWorkspace(ctx)
	if err != nil {
		return nil, logger.Wrap(err)
	}
	project, err := ws.LoadProjectFromName(ctx, action.Project)
	if err != nil {
		return nil, logger.Wrap(err)
	}

	w, err := project.NewEnvironment(ctx, action.AddEnvironment)
	if err != nil {
		return nil, logger.Wrap(err)
	}
	err = project.Save(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save")
	}
	return w, nil
}

func init() {
	actions.RegisterFactory(AddEnvironment, actions.Wrap[*AddEnvironmentAction]())
}
