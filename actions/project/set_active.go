package project

import (
	"context"
	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/shared"

	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"

	"github.com/codefly-dev/core/configurations"
)

const SetProjectActiveKind = "project.set_as_active"

type SetProjectActive = v1actions.SetProjectActive

type SetProjectActiveAction struct {
	*SetProjectActive
}

func NewActionSetProjectActive(in *SetProjectActive) *SetProjectActiveAction {
	in.Kind = SetProjectActiveKind
	return &SetProjectActiveAction{
		SetProjectActive: in,
	}
}

var _ actions.Action = (*SetProjectActiveAction)(nil)

func (action *SetProjectActiveAction) Run(ctx context.Context) (any, error) {
	logger := shared.GetBaseLogger(ctx).With("SetProjectActiveAction")
	w, err := configurations.ActiveWorkspace(ctx)
	if err != nil {
		return nil, logger.Wrap(err)
	}

	err = w.SetProjectActive(ctx, action.SetProjectActive)
	if err != nil {
		return nil, logger.Wrap(err)
	}
	w, err = w.Reload(ctx)
	if err != nil {
		return nil, logger.Wrap(err)
	}
	// Return the project
	project, err := w.LoadActiveProject(ctx)
	if err != nil {
		return nil, logger.Wrap(err)
	}
	return project, nil
}

func init() {
	actions.RegisterFactory(SetProjectActiveKind, actions.Wrap[*SetProjectActiveAction]())
}
