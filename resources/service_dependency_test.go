package resources_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

// Services sharing a name across different modules must be tracked as distinct
// dependencies — ExistsDependency keys on Module as well as Name.
func TestAddDependencySameNameDifferentModule(t *testing.T) {
	ctx := context.Background()
	svc := &resources.Service{Name: "consumer"}

	require.NoError(t, svc.AddDependency(ctx, &resources.ServiceIdentity{Name: "foo", Module: "module-a"}, nil))
	require.NoError(t, svc.AddDependency(ctx, &resources.ServiceIdentity{Name: "foo", Module: "module-b"}, nil))

	require.Len(t, svc.ServiceDependencies, 2)

	_, okA := svc.ExistsDependency(&resources.ServiceIdentity{Name: "foo", Module: "module-a"})
	_, okB := svc.ExistsDependency(&resources.ServiceIdentity{Name: "foo", Module: "module-b"})
	require.True(t, okA)
	require.True(t, okB)

	require.NoError(t, svc.AddDependency(ctx, &resources.ServiceIdentity{Name: "foo", Module: "module-a"}, nil))
	require.Len(t, svc.ServiceDependencies, 2)
}
