package application

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/resources"

	actionsv0 "github.com/codefly-dev/core/generated/go/codefly/actions/v0"
	"github.com/codefly-dev/core/wool"
)

type AddApplicationAction struct {
	*AddApplication
}

func (action *AddApplicationAction) Command() string {
	agent := resources.AgentFromProto(action.Agent)
	return fmt.Sprintf("codefly add application %s --agent=%s", action.Name, agent.Identifier())
}

type AddApplication = actionsv0.AddApplication

func NewActionAddApplication(ctx context.Context, in *AddApplication) (*AddApplicationAction, error) {
	w := wool.Get(ctx).In("actions.application.NewActionAddApplication")
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = actions.AddApplication
	return &AddApplicationAction{
		AddApplication: in,
	}, nil
}

var _ actions.Action = (*AddApplicationAction)(nil)

func (action *AddApplicationAction) Run(ctx context.Context, space *actions.Space) (any, error) {
	w := wool.Get(ctx).In("actions.application.AddApplicationAction.Run")

	app, err := space.Module.NewApplication(ctx, action.AddApplication)
	if err != nil {
		return nil, w.Wrapf(err, "cannot module.NewApplication")
	}

	return app, nil
}

func init() {
	actions.RegisterBuilder(actions.AddApplication, actions.Wrap[*AddApplicationAction]())
}
