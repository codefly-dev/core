package application

import (
	"context"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/actions/actions"

	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"

	"github.com/codefly-dev/core/configurations"
)

const SetApplicationActiveKind = "application.activate"

type SetApplicationActive = v1actions.SetApplicationActive

type SetApplicationActiveAction struct {
	*SetApplicationActive
}

func (action *SetApplicationActiveAction) Command() string {
	return "codefly switch application"
}

func NewActionSetApplicationActive(ctx context.Context, in *SetApplicationActive) (*SetApplicationActiveAction, error) {
	logger := shared.GetLogger(ctx).With(shared.Type(in))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, logger.Wrap(err)
	}
	in.Kind = SetApplicationActiveKind
	return &SetApplicationActiveAction{
		SetApplicationActive: in,
	}, nil
}

var _ actions.Action = (*SetApplicationActiveAction)(nil)

func (action *SetApplicationActiveAction) Run(ctx context.Context) (any, error) {
	logger := shared.GetLogger(ctx).With("SetApplicationActiveAction<%s>", action.Name)
	if action.InProject == "" {
		return nil, logger.Errorf("missing project in action")
	}

	w, err := configurations.LoadWorkspace(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot get active workspace")
	}

	project, err := w.LoadProjectFromName(ctx, action.InProject)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load project from name: %s", action.InProject)
	}

	err = project.SetActiveApplication(ctx, action.Name)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot set active application: %s", action.Name)
	}

	err = project.Save(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save project")
	}

	// reload
	project, err = w.ReloadProject(ctx, project)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot reload project")
	}

	return project.LoadActiveApplication(ctx)
}

func init() {
	actions.RegisterFactory(SetApplicationActiveKind, actions.Wrap[*SetApplicationActiveAction]())
}
