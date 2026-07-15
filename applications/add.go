package applications

import (
	"context"
	"fmt"

	"github.com/codefly-dev/core/actions/actions"
	actionapplication "github.com/codefly-dev/core/actions/application"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

type AddOutput struct {
	ReadMe string
}

func Add(ctx context.Context, workspace *resources.Workspace, module *resources.Module, input *actionapplication.AddApplication) (*AddOutput, error) {
	if workspace == nil {
		return nil, fmt.Errorf("workspace is nil")
	}
	if module == nil {
		return nil, fmt.Errorf("module is nil")
	}
	if input == nil {
		return nil, fmt.Errorf("add application input is nil")
	}
	w := wool.Get(ctx).In("applications.Add", wool.Field("workspace", workspace.Name), wool.Field("module", module.Name), wool.Field("input", input))

	action, err := actionapplication.NewActionAddApplication(ctx, input)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create action")
	}

	out, err := actions.Run(ctx, action, &actions.Space{Module: module})
	if err != nil {
		return nil, w.Wrapf(err, "cannot run AddApplication action")
	}

	app, err := actions.As[resources.Application](out)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get application back from action output")
	}

	app.SetModule(module.Name)

	output := &AddOutput{
		ReadMe: "# " + app.Name + "\n\nApplication created successfully.\n",
	}

	return output, nil
}
