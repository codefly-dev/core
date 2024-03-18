package project

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/wool"

	"github.com/bufbuild/protovalidate-go"

	"github.com/codefly-dev/core/actions/actions"
	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"

	"github.com/codefly-dev/core/configurations"
)

const AddProjectKind = "project.add"

type AddProject = actionsv0.NewProject
type NewProjectAction struct {
	*AddProject
}

func (action *NewProjectAction) Command() string {
	return fmt.Sprintf("codefly add project %s", action.Name)
}

func NewActionNewProject(ctx context.Context, in *AddProject) (*NewProjectAction, error) {
	w := wool.Get(ctx).In("project.NewActionNewProject")
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = AddProjectKind
	return &NewProjectAction{
		AddProject: in,
	}, nil
}

var _ actions.Action = (*NewProjectAction)(nil)

func (action *NewProjectAction) Run(ctx context.Context) (any, error) {
	w := wool.Get(ctx).In("project.NewProjectAction.Run")

	// Validate
	v, err := protovalidate.New()
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = v.Validate(action.AddProject)
	if err != nil {
		return nil, w.Wrap(err)
	}

	project, err := configurations.CreateProject(ctx, action.AddProject)
	if err != nil {
		return nil, err
	}

	return project, nil
}

func init() {
	actions.RegisterBuilder(AddProjectKind, actions.Wrap[*NewProjectAction]())
}
