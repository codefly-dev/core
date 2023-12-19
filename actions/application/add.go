package application

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations"

	"github.com/codefly-dev/core/actions/actions"

	actionsv1 "github.com/codefly-dev/core/generated/go/actions/v1"
)

const AddApplicationKind = "application.add"

type AddApplication = actionsv1.AddApplication
type AddApplicationAction struct {
	*AddApplication
}

func (action *AddApplicationAction) Command() string {
	return fmt.Sprintf("codefly add application %s", action.Name)
}

func NewActionAddApplication(ctx context.Context, in *AddApplication) (*AddApplicationAction, error) {
	w := wool.Get(ctx).In("NewActionAddApplication", wool.Field("name", in.Name))
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
	w := wool.Get(ctx).In("AddApplicationAction.Run", wool.Field("name", action.Name))

	if action.Project == "" {
		return nil, w.NewError("missing project in action")
	}

	workspace, err := configurations.LoadWorkspace(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}

	project, err := workspace.LoadProjectFromName(ctx, action.Project)
	if err != nil {
		return nil, w.Wrap(err)
	}

	application, err := project.NewApplication(ctx, action.AddApplication)
	if err != nil {
		return nil, w.Wrap(err)
	}

	err = project.SetActiveApplication(ctx, application.Name)
	if err != nil {
		return nil, w.Wrap(err)
	}

	err = project.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save project")
	}

	return application, nil
}

func init() {
	actions.RegisterFactory(AddApplicationKind, actions.Wrap[*AddApplicationAction]())
}
