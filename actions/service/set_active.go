package service

import (
	"context"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/actions/actions"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
)

const SetServiceActiveKind = "service.activate"

type SetServiceActive = actionsv0.SetServiceActive
type SetServiceActiveAction struct {
	*SetServiceActive
}

func (action *SetServiceActiveAction) Command() string {
	return "codefly switch service"
}

func NewActionSetServiceActive(ctx context.Context, in *SetServiceActive) (*SetServiceActiveAction, error) {
	w := wool.Get(ctx).In("NewActionSetServiceActive", wool.NameField(in.Name))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = SetServiceActiveKind
	return &SetServiceActiveAction{
		SetServiceActive: in,
	}, nil
}

var _ actions.Action = (*SetServiceActiveAction)(nil)

func (action *SetServiceActiveAction) Run(ctx context.Context) (any, error) {
	w := wool.Get(ctx).In("SetServiceActiveAction.Run", wool.NameField(action.Name))

	ws, err := configurations.LoadWorkspace(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}

	project, err := ws.LoadProjectFromName(ctx, action.Project)
	if err != nil {
		return nil, w.Wrap(err)
	}

	app, err := project.LoadApplicationFromName(ctx, action.Application)
	if err != nil {
		return nil, w.Wrap(err)
	}

	err = app.SetActiveService(ctx, action.Name)
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = app.Save(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	return app, nil
}

func init() {
	actions.RegisterFactory(SetServiceActiveKind, actions.Wrap[*SetServiceActiveAction]())
}
