package project

import (
	"context"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/shared"

	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"

	"github.com/codefly-dev/core/configurations"
)

const AddProjectKind = "project.add"

type AddProject = v1actions.AddProject

type AddProjectAction struct {
	*AddProject
}

func NewActionAddProject(in *AddProject) *AddProjectAction {
	in.Kind = AddProjectKind
	return &AddProjectAction{
		AddProject: in,
	}
}

var _ actions.Action = (*AddProjectAction)(nil)

func (action *AddProjectAction) Run(ctx context.Context) (any, error) {
	logger := shared.GetBaseLogger(ctx).With("AddProjectAction<%s>", action.Name)

	w, err := configurations.ActiveWorkspace(ctx)
	if err != nil {
		return nil, logger.Wrap(err)
	}

	project, err := w.NewProject(ctx, action.AddProject)
	if err != nil {
		return nil, err
	}

	err = w.SetProjectActive(ctx, &v1actions.SetProjectActive{Name: project.Name})
	if err != nil {
		return nil, logger.Wrapf(err, "cannot set project as active")
	}

	return project, nil
}

func init() {
	actions.RegisterFactory(AddProjectKind, actions.Wrap[*AddProjectAction]())
}
