package service

import (
	"context"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/actions/actions"

	v1actions "github.com/codefly-dev/core/generated/v1/go/proto/actions"
)

const SetServiceActiveKind = "service.activate"

type SetServiceActive = v1actions.SetServiceActive

type SetServiceActiveAction struct {
	*SetServiceActive
}

func (action *SetServiceActiveAction) Command() string {
	return "codefly switch service"
}

func NewActionSetServiceActive(ctx context.Context, in *SetServiceActive) (*SetServiceActiveAction, error) {
	logger := shared.GetLogger(ctx).With(shared.ProtoType(in))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, logger.Wrap(err)
	}
	in.Kind = SetServiceActiveKind
	return &SetServiceActiveAction{
		SetServiceActive: in,
	}, nil
}

var _ actions.Action = (*SetServiceActiveAction)(nil)

func (action *SetServiceActiveAction) Run(ctx context.Context) (any, error) {
	return nil, nil
}

func init() {
	actions.RegisterFactory(SetServiceActiveKind, actions.Wrap[*SetServiceActiveAction]())
}
