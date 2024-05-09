package service

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/resources"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
	"github.com/codefly-dev/core/wool"
)

type AddServiceAction struct {
	*AddService
}

func (action *AddServiceAction) Command() string {
	agent := resources.AgentFromProto(action.Agent)
	return fmt.Sprintf("codefly add service %s --agent=%s", action.Name, agent.Identifier())
}

type AddService = actionsv0.AddService

func NewActionAddService(ctx context.Context, in *AddService) (*AddServiceAction, error) {
	w := wool.Get(ctx).In("actions.service.NewActionAddService")
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = actions.AddService
	return &AddServiceAction{
		AddService: in,
	}, nil
}

var _ actions.Action = (*AddServiceAction)(nil)

func (action *AddServiceAction) Run(ctx context.Context, space *actions.Space) (any, error) {
	w := wool.Get(ctx).In("actions.service.AddServiceAction.Run")

	service, err := space.Module.NewService(ctx, action.AddService)
	if err != nil {
		return nil, w.Wrapf(err, "cannot module.NewService")
	}

	return service, nil
}

func init() {
	actions.RegisterBuilder(actions.AddService, actions.Wrap[*AddServiceAction]())
}
