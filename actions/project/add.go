package project

import (
	"context"
	"fmt"
	"github.com/bufbuild/protovalidate-go"

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

func (action *AddProjectAction) Command() string {
	return fmt.Sprintf("codefly add project %s", action.Name)
}

func NewActionAddProject(ctx context.Context, in *AddProject) (*AddProjectAction, error) {
	logger := shared.GetLogger(ctx).With(shared.Type(in))
	if err := actions.Validate(ctx, in); err != nil {
		return nil, logger.Wrap(err)
	}
	in.Kind = AddProjectKind
	return &AddProjectAction{
		AddProject: in,
	}, nil
}

var _ actions.Action = (*AddProjectAction)(nil)

func (action *AddProjectAction) Run(ctx context.Context) (any, error) {
	logger := shared.GetLogger(ctx).With("AddProjectAction<%s>", action.Name)

	// Validate
	v, err := protovalidate.New()
	if err != nil {
		return nil, logger.Wrap(err)
	}
	err = v.Validate(action.AddProject)
	if err != nil {
		return nil, logger.Wrap(err)
	}

	w, err := configurations.LoadWorkspace(ctx)
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
