package project

import (
	"context"
	"fmt"
	"github.com/codefly-dev/core/wool"

	"github.com/bufbuild/protovalidate-go"

	"github.com/codefly-dev/core/actions/actions"
	actionsv1 "github.com/codefly-dev/core/generated/go/actions/v1"

	"github.com/codefly-dev/core/configurations"
)

const AddProjectKind = "project.add"

type AddProject = actionsv1.AddProject
type AddProjectAction struct {
	*AddProject
}

func (action *AddProjectAction) Command() string {
	return fmt.Sprintf("codefly add project %s", action.Name)
}

func NewActionAddProject(ctx context.Context, in *AddProject) (*AddProjectAction, error) {
	w := wool.Get(ctx).In("project.NewActionAddProject")
	if err := actions.Validate(ctx, in); err != nil {
		return nil, w.Wrap(err)
	}
	in.Kind = AddProjectKind
	return &AddProjectAction{
		AddProject: in,
	}, nil
}

var _ actions.Action = (*AddProjectAction)(nil)

func (action *AddProjectAction) Run(ctx context.Context) (any, error) {
	w := wool.Get(ctx).In("project.AddProjectAction.Run")

	// Validate
	v, err := protovalidate.New()
	if err != nil {
		return nil, w.Wrap(err)
	}
	err = v.Validate(action.AddProject)
	if err != nil {
		return nil, w.Wrap(err)
	}

	workspace, err := configurations.LoadWorkspace(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}

	project, err := workspace.NewProject(ctx, action.AddProject)
	if err != nil {
		return nil, err
	}

	err = workspace.SetProjectActive(ctx, &actionsv1.SetProjectActive{
		Name: project.Name,
	})
	if err != nil {
		return nil, w.Wrapf(err, "cannot set project as active")
	}

	return project, nil
}

func init() {
	actions.RegisterFactory(AddProjectKind, actions.Wrap[*AddProjectAction]())
}
