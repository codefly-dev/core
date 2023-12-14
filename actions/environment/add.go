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

func (action *AddEnvironmentAction) Command() string {
	return "TODO"
}

func NewActionAddEnvironment(ctx context.Context, in *v1actions.AddEnvironment) (*AddEnvironmentAction, error) {
	logger := shared.GetLogger(ctx).With(shared.ProtoType(in))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, logger.Wrap(err)
	}
	in.Kind = AddEnvironment
	return &AddEnvironmentAction{
		AddEnvironment: in,
	}, nil
}

var _ actions.Action = (*AddEnvironmentAction)(nil)

func (action *AddEnvironmentAction) Run(ctx context.Context) (any, error) {
	logger := shared.GetLogger(ctx).With("AddEnvironmentAction")
	// Get project
	ws, err := configurations.LoadWorkspace(ctx)
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
