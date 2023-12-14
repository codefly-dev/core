package service

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/actions/actions"

	v1actions "github.com/codefly-dev/core/generated/v1/go/proto/actions"
)

const AddServiceDependencyKind = "service.add_dependency"

type AddServiceDependencyAction struct {
	*AddServiceDependency
}

func (action *AddServiceDependencyAction) Command() string {
	return fmt.Sprintf("TODO")
}

type AddServiceDependency = v1actions.AddServiceDependency

func NewActionAddServiceDependency(ctx context.Context, in *AddServiceDependency) (*AddServiceDependencyAction, error) {
	logger := shared.GetLogger(ctx).With(shared.ProtoType(in))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, logger.Wrap(err)
	}
	in.Kind = AddServiceDependencyKind
	return &AddServiceDependencyAction{
		AddServiceDependency: in,
	}, nil
}

var _ actions.Action = (*AddServiceDependencyAction)(nil)

func (action *AddServiceDependencyAction) Run(ctx context.Context) (any, error) {
	logger := shared.GetLogger(ctx).With("AddServiceDependencyAction")

	ws, err := configurations.LoadWorkspace(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot get current workspace")
	}

	project, err := ws.LoadProjectFromName(ctx, action.Project)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load project %s", action.Project)
	}

	app, err := project.LoadApplicationFromName(ctx, action.Application)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load application %s", action.Application)
	}

	service, err := app.LoadServiceFromName(ctx, action.Name)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load service %s", action.Name)
	}

	appDep, err := project.LoadApplicationFromName(ctx, action.DependencyApplication)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load dependent application %s", action.DependencyApplication)
	}
	serviceDependency, err := appDep.LoadServiceFromName(ctx, action.DependencyName)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load dependent service %s", action.DependencyName)
	}
	// Validate that the endpoints exists
	unknowns, err := serviceDependency.HasEndpoints(ctx, action.Endpoints)
	if err != nil {
		return nil, logger.Wrapf(err, "unknown endpoints %s for service %s", unknowns, action.DependencyName)
	}
	dependencyEndpoints, err := serviceDependency.EndpointsFromNames(action.Endpoints)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot get endpoints %s for service %s", action.Endpoints, action.DependencyName)
	}
	err = service.AddDependency(ctx, serviceDependency, dependencyEndpoints)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot add dependency %s to service %s", action.DependencyName, action.Name)
	}
	err = service.Save(ctx)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot save service %s", action.Name)
	}

	return service, nil
}

func init() {
	actions.RegisterFactory(AddServiceDependencyKind, actions.Wrap[*AddServiceDependencyAction]())
}
