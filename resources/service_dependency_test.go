package resources_test

import (
	"context"
	"os"
	"path/filepath"
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

// A duplicate entry is rejected where it is read. Left to the loader, it
// surfaced far downstream as an internal graph-invariant error naming nothing
// the author could act on, and one of the two endpoint lists was discarded.
func TestLoadServiceRejectsDuplicateDependency(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.ServiceConfigurationName), []byte(`kind: service
name: orders
version: 0.0.1
agent:
    kind: runtime::service
    name: go-grpc
    version: 0.0.16
    publisher: codefly.ai
service-dependencies:
    - name: postgres
      module: data
      endpoints:
        - name: tcp
    - name: postgres
      module: data
      endpoints:
        - name: admin
`), 0o644))

	_, err := resources.LoadServiceFromDir(ctx, dir)
	require.Error(t, err)
	require.Contains(t, err.Error(), `duplicate service dependency "data/postgres"`)
}

// The module defaults to the declaring service's own module, so an entry that
// spells it out and one that omits it name the same dependency. Only a
// module-based load knows the owning module, so that is the path under test.
func TestLoadServiceRejectsDuplicateDependencyAcrossImplicitModule(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	serviceDir := filepath.Join(dir, "services", "orders")
	require.NoError(t, os.MkdirAll(serviceDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.ModuleConfigurationName), []byte(`kind: module
name: api
services:
    - name: orders
`), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(serviceDir, resources.ServiceConfigurationName), []byte(`kind: service
name: orders
version: 0.0.1
agent:
    kind: runtime::service
    name: go-grpc
    version: 0.0.16
    publisher: codefly.ai
service-dependencies:
    - name: cache
    - name: cache
      module: api
`), 0o644))

	module, err := resources.LoadModuleFromDir(ctx, dir)
	require.NoError(t, err)

	_, err = module.LoadServiceFromName(ctx, "orders")
	require.Error(t, err)
	require.Contains(t, err.Error(), `duplicate service dependency "api/cache"`)
}
