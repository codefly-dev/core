package application

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations"

	"github.com/codefly-dev/core/actions/actions"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
)

const AddApplicationKind = "application.add"

type AddApplication = actionsv0.NewApplication
type AddApplicationAction struct {
	*AddApplication
}

func (action *AddApplicationAction) Command() string {
	return fmt.Sprintf("codefly add application %s", action.Name)
}

func NewActionAddApplication(ctx context.Context, in *AddApplication) (*AddApplicationAction, error) {
	w := wool.Get(ctx).In("NewActionAddApplication", wool.NameField(in.Name))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = AddApplicationKind
	return &AddApplicationAction{
		AddApplication: in,
	}, nil
}

var _ actions.Action = (*AddApplicationAction)(nil)

func (action *AddApplicationAction) Run(ctx context.Context) (any, error) {
	w := wool.Get(ctx).In("AddApplicationAction.Run", wool.NameField(action.Name))

	project, err := configurations.LoadProjectFromDir(ctx, action.ProjectPath)

	if err != nil {
		return nil, w.Wrap(err)
	}

	application, err := project.NewApplication(ctx, action.AddApplication)

	if err != nil {
		return nil, w.Wrap(err)
	}

	return application, nil
}

func init() {
	actions.RegisterBuilder(AddApplicationKind, actions.Wrap[*AddApplicationAction]())
}
