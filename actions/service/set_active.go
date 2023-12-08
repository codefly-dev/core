package service

import (
	"context"

	"github.com/codefly-dev/core/actions/actions"

	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
)

const SetServiceActiveKind = "service.activate"

type SetServiceActive = v1actions.SetServiceActive

type SetServiceActiveAction struct {
	*SetServiceActive
}

func NewActionSetServiceActive(in *SetServiceActive) *SetServiceActiveAction {
	in.Kind = SetServiceActiveKind
	return &SetServiceActiveAction{
		SetServiceActive: in,
	}
}

var _ actions.Action = (*SetServiceActiveAction)(nil)

func (action *SetServiceActiveAction) Run(ctx context.Context) (any, error) {
	return nil, nil
}

func init() {
	actions.RegisterFactory(SetServiceActiveKind, actions.Wrap[*SetServiceActiveAction]())
}
