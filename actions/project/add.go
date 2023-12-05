package project

import (
	"context"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/shared"

	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"

	"github.com/codefly-dev/core/configurations"
)

const AddProject = "project.add"

type AddProjectAction struct {
	*v1actions.AddProject
}

func NewAddProjectAction(in *v1actions.AddProject) *AddProjectAction {
	in.Kind = AddProject
	return &AddProjectAction{
		AddProject: in,
	}
}

var _ actions.Action = (*AddProjectAction)(nil)

func (action *AddProjectAction) Run(ctx context.Context) (any, error) {
	logger := shared.GetBaseLogger(ctx).With("AddProjectAction")
	w, err := configurations.CurrentWorkspace(ctx)
	if err != nil {
		return nil, logger.Wrap(err)
	}
	project, err := w.NewProject(ctx, action.AddProject)
	if err != nil {
		return nil, err
	}
	return project, nil
}

func init() {
	actions.RegisterFactory(AddProject, actions.Wrap[*AddProjectAction]())
}
