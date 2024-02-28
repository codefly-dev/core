package service

import (
	"context"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/configurations"

	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
)

const AddServiceDependencyKind = "service.add_dependency"

type AddServiceDependencyAction struct {
	*AddServiceDependency
}

func (action *AddServiceDependencyAction) Command() string {
	return "TODO"
}

type AddServiceDependency = actionsv0.AddServiceDependency

func NewActionAddServiceDependency(ctx context.Context, in *AddServiceDependency) (*AddServiceDependencyAction, error) {
	w := wool.Get(ctx).In("NewActionAddServiceDependency", wool.NameField(in.Name))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = AddServiceDependencyKind
	return &AddServiceDependencyAction{
		AddServiceDependency: in,
	}, nil
}

var _ actions.Action = (*AddServiceDependencyAction)(nil)

func (action *AddServiceDependencyAction) Run(ctx context.Context) (any, error) {
	w := wool.Get(ctx).In("AddServiceDependencyAction.Run", wool.NameField(action.Name))

	project, err := configurations.LoadProjectFromDirUnsafe(ctx, action.ProjectPath)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load project %s", action.ProjectPath)
	}

	app, err := project.LoadApplicationFromName(ctx, action.Application)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load application %s", action.Application)
	}

	service, err := app.LoadServiceFromName(ctx, action.Name)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load service %s", action.Name)
	}

	appDep, err := project.LoadApplicationFromName(ctx, action.DependencyApplication)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load dependent application %s", action.DependencyApplication)
	}
	serviceDependency, err := appDep.LoadServiceFromName(ctx, action.DependencyName)
	if err != nil {
		return nil, w.Wrapf(err, "cannot load dependent service %s", action.DependencyName)
	}
	// Validate that the endpoints exists
	unknowns, err := serviceDependency.HasEndpoints(ctx, action.Endpoints)
	if err != nil {
		return nil, w.Wrapf(err, "unknown endpoints %s for service %s", unknowns, action.DependencyName)
	}
	dependencyEndpoints, err := serviceDependency.EndpointsFromNames(action.Endpoints)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get endpoints %s for service %s", action.Endpoints, action.DependencyName)
	}
	err = service.AddDependency(ctx, serviceDependency, dependencyEndpoints)
	if err != nil {
		return nil, w.Wrapf(err, "cannot add dependency %s to service %s", action.DependencyName, action.Name)
	}
	err = service.Save(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot save service %s", action.Name)
	}

	return service, nil
}

func init() {
	actions.RegisterBuilder(AddServiceDependencyKind, actions.Wrap[*AddServiceDependencyAction]())
}
