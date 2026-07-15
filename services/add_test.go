package services

import (
	"context"
	"testing"

	actionservice "github.com/codefly-dev/core/actions/service"
	"github.com/codefly-dev/core/resources"
)

func TestAddRejectsNilBoundaries(t *testing.T) {
	ctx := context.Background()
	workspace := &resources.Workspace{Name: "workspace"}
	module := &resources.Module{Kind: resources.ModuleKind, Name: "module"}
	input := &actionservice.AddService{Name: "service"}

	for name, test := range map[string]func() error{
		"workspace": func() error {
			_, err := Add(ctx, nil, module, input, nil)
			return err
		},
		"module": func() error {
			_, err := Add(ctx, workspace, nil, input, nil)
			return err
		},
		"input": func() error {
			_, err := Add(ctx, workspace, module, nil, nil)
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
