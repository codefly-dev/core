package applications

import (
	"context"
	"testing"

	actionapplication "github.com/codefly-dev/core/actions/application"
	"github.com/codefly-dev/core/resources"
)

func TestAddRejectsNilBoundaries(t *testing.T) {
	ctx := context.Background()
	workspace := &resources.Workspace{Name: "workspace"}
	module := &resources.Module{Kind: resources.ModuleKind, Name: "module"}
	input := &actionapplication.AddApplication{Name: "application"}

	for name, test := range map[string]func() error{
		"workspace": func() error {
			_, err := Add(ctx, nil, module, input)
			return err
		},
		"module": func() error {
			_, err := Add(ctx, workspace, nil, input)
			return err
		},
		"input": func() error {
			_, err := Add(ctx, workspace, module, nil)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			if err := test(); err == nil {
				t.Fatal("nil boundary returned success")
			}
		})
	}
}
