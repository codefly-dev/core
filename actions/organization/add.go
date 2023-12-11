package organization

import (
	"context"
	"github.com/bufbuild/protovalidate-go"
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/actions/actions"

	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
)

const AddOrganizationKind = "organization.add"

type AddOrganization = v1actions.AddOrganization

type AddOrganizationAction struct {
	*AddOrganization
}

func (action *AddOrganizationAction) Command() string {
	return "TODO"
}

func NewActionAddOrganization(ctx context.Context, in *AddOrganization) (*AddOrganizationAction, error) {
	logger := shared.GetLogger(ctx).With(shared.Type(in))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, logger.Wrap(err)
	}
	in.Kind = AddOrganizationKind
	return &AddOrganizationAction{
		AddOrganization: in,
	}, nil
}

var _ actions.Action = (*AddOrganizationAction)(nil)

func (action *AddOrganizationAction) Run(ctx context.Context) (any, error) {
	logger := shared.GetLogger(ctx).With("AddOrganizationAction<%s>", action.Name)

	// Validate
	v, err := protovalidate.New()
	if err != nil {
		return nil, logger.Wrap(err)
	}
	err = v.Validate(action.AddOrganization)
	if err != nil {
		return nil, logger.Wrap(err)
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
