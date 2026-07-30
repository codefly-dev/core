package service_test

import (
	"testing"

	"github.com/codefly-dev/core/actions/service"
	actionsv0 "github.com/codefly-dev/core/generated/go/codefly/actions/v0"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/stretchr/testify/require"
)

func TestCommandRendersAgentWithoutPanicOnUnregisteredKind(t *testing.T) {
	action := &service.AddServiceAction{
		AddService: &actionsv0.AddService{
			Name: "my-service",
			// Kind is left UNKNOWN: rendering the equivalent CLI command must not
			// depend on a registered/validatable agent kind, and must never panic.
			Agent: &basev0.Agent{Publisher: "codefly.dev", Name: "go-grpc", Version: "1.2.3"},
		},
	}
	require.NotPanics(t, func() {
		require.Equal(t, "codefly add service my-service --agent=codefly.dev/go-grpc:1.2.3", action.Command())
	})
}
