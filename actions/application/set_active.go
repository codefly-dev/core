package application

import (
	"context"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/actions/actions"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"

	"github.com/codefly-dev/core/configurations"
)

const SetApplicationActiveKind = "application.activate"

type SetApplicationActive = actionsv0.SetApplicationActive
type SetApplicationActiveAction struct {
	*SetApplicationActive
}

func (action *SetApplicationActiveAction) Command() string {
	return "codefly switch application"
}

func NewActionSetApplicationActive(ctx context.Context, in *SetApplicationActive) (*SetApplicationActiveAction, error) {
	w := wool.Get(ctx).In("NewActionSetApplicationActive", wool.NameField(in.Name))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = SetApplicationActiveKind
	return &SetApplicationActiveAction{
		SetApplicationActive: in,
	}, nil
}

var _ actions.Action = (*SetApplicationActiveAction)(nil)

func (action *SetApplicationActiveAction) Run(ctx context.Context) (any, error) {
	w := wool.Get(ctx).In("SetApplicationActiveAction.Run", wool.NameField(action.Name))
	if action.Project == "" {
		return nil, w.NewError("missing project in action")
	}

	workspace, err := configurations.LoadWorkspace(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get active workspace")
	}

	project, err := workspace.LoadProjectFromName(ctx, action.Project)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load project from name: %s", action.Project)
	}

	err = project.SetActiveApplication(ctx, action.Name)
	if err != nil {
		return nil, w.Wrapf(err, "cannot set active application: %s", action.Name)
	}

	err = project.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save project")
	}

	// reload
	project, err = workspace.ReloadProject(ctx, project)
	if err != nil {
		return nil, w.Wrapf(err, "cannot reload project")
	}

	return project.LoadActiveApplication(ctx)
}

func init() {
	actions.RegisterFactory(SetApplicationActiveKind, actions.Wrap[*SetApplicationActiveAction]())
}
