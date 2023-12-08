package application

import (
	"context"
	"github.com/codefly-dev/core/configurations"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/actions/actions"

	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
)

const AddApplicationKind = "application.add"

type AddApplication = v1actions.AddApplication

type AddApplicationAction struct {
	*AddApplication
}

func NewActionAddApplication(in *AddApplication) *AddApplicationAction {
	in.Kind = AddApplicationKind
	return &AddApplicationAction{
		AddApplication: in,
	}
}

var _ actions.Action = (*AddApplicationAction)(nil)

func (action *AddApplicationAction) Run(ctx context.Context) (any, error) {
	logger := shared.GetBaseLogger(ctx).With("AddApplicationAction<%s>", action.Name)

	if action.Project == "" {
		return nil, logger.Errorf("missing project in action")
	}

	w, err := configurations.ActiveWorkspace(ctx)
	if err != nil {
		return nil, logger.Wrap(err)
	}

	project, err := w.LoadProjectFromName(ctx, action.Project)
	if err != nil {
		return nil, logger.Wrap(err)
	}

	application, err := project.NewApplication(ctx, action.AddApplication)
	if err != nil {
		return nil, logger.Wrap(err)
	}

	err = application.Save(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save application")
	}

	err = project.SetActiveApplication(ctx, application.Name)
	if err != nil {
		return nil, logger.Wrap(err)
	}

	err = project.Save(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save project")
	}

	return application, nil
}

func init() {
	actions.RegisterFactory(AddApplicationKind, actions.Wrap[*AddApplicationAction]())
}
