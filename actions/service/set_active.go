package service

import (
	"context"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/actions/actions"

	actionsv1 "github.com/codefly-dev/core/generated/go/actions/v1"
)

const SetServiceActiveKind = "service.activate"

type SetServiceActive = actionsv1.SetServiceActive
type SetServiceActiveAction struct {
	*SetServiceActive
}

func (action *SetServiceActiveAction) Command() string {
	return "codefly switch service"
}

func NewActionSetServiceActive(ctx context.Context, in *SetServiceActive) (*SetServiceActiveAction, error) {
	w := wool.Get(ctx).In("NewActionSetServiceActive", wool.Field("name", in.Name))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = SetServiceActiveKind
	return &SetServiceActiveAction{
		SetServiceActive: in,
	}, nil
}

var _ actions.Action = (*SetServiceActiveAction)(nil)

func (action *SetServiceActiveAction) Run(_ context.Context) (any, error) {
	return nil, nil
}

func init() {
	actions.RegisterFactory(SetServiceActiveKind, actions.Wrap[*SetServiceActiveAction]())
}
