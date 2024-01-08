package organization

import (
	"context"

	"github.com/codefly-dev/core/wool"

	"github.com/bufbuild/protovalidate-go"
	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/configurations"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
)

const AddOrganizationKind = "organization.add"

type AddOrganization = actionsv0.AddOrganization
type AddOrganizationAction struct {
	*AddOrganization
}

func (action *AddOrganizationAction) Command() string {
	return "TODO"
}

func NewActionAddOrganization(ctx context.Context, in *AddOrganization) (*AddOrganizationAction, error) {
	w := wool.Get(ctx).In("NewActionAddOrganization", wool.NameField(in.Name))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = AddOrganizationKind
	return &AddOrganizationAction{
		AddOrganization: in,
	}, nil
}

var _ actions.Action = (*AddOrganizationAction)(nil)

func (action *AddOrganizationAction) Run(ctx context.Context) (any, error) {
	w := wool.Get(ctx).In("AddOrganizationAction.Run", wool.NameField(action.Name))
	// Validate
	v, err := protovalidate.New()
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = v.Validate(action.AddOrganization)
	if err != nil {
		return nil, w.Wrap(err)
	}

	org := &configurations.Organization{
		Name:   action.Name,
		Domain: action.Domain,
	}
	return org, nil
}

func init() {
	actions.RegisterFactory(AddOrganizationKind, actions.Wrap[*AddOrganizationAction]())
}
