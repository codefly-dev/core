package project

import (
	"context"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/shared"

	v1actions "github.com/codefly-dev/core/generated/v1/go/proto/actions"

	"github.com/codefly-dev/core/configurations"
)

const SetProjectActiveKind = "project.set_as_active"

type SetProjectActive = v1actions.SetProjectActive

type SetProjectActiveAction struct {
	*SetProjectActive
}

func (action *SetProjectActiveAction) Command() string {
	return "codefly switch project"
}

func NewActionSetProjectActive(ctx context.Context, in *SetProjectActive) (*SetProjectActiveAction, error) {
	logger := shared.GetLogger(ctx).With(shared.ProtoType(in))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, logger.Wrap(err)
	}
	in.Kind = SetProjectActiveKind
	return &SetProjectActiveAction{
		SetProjectActive: in,
	}, nil
}

var _ actions.Action = (*SetProjectActiveAction)(nil)

func (action *SetProjectActiveAction) Run(ctx context.Context) (any, error) {
	logger := shared.GetLogger(ctx).With("SetProjectActiveAction")
	w, err := configurations.LoadWorkspace(ctx)
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
