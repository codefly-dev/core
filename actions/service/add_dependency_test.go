package service_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	"github.com/codefly-dev/core/actions/service"
	actionsv0 "github.com/codefly-dev/core/generated/go/codefly/actions/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func write(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

// visibilityWorkspace writes a modules-layout workspace with a vault service
// exposing an "internal" endpoint allow-listed to the platform module only,
// plus one consuming service in platform (allowed) and one in web (denied).
func visibilityWorkspace(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	write(t, filepath.Join(dir, "workspace.codefly.yaml"), `name: codefly-platform
layout: modules
modules:
    - name: vault
    - name: platform
    - name: web
`)
	consumer := func(name string) string {
		return `kind: service
name: ` + name + `
version: 0.0.0
agent:
    kind: runtime::service
    name: go-grpc
    version: 0.0.16
    publisher: codefly.ai
endpoints:
    - name: grpc
      api: grpc
`
	}
	write(t, filepath.Join(dir, "modules", "vault", "module.codefly.yaml"), "kind: module\nname: vault\nservices:\n    - name: secrets\n")
	write(t, filepath.Join(dir, "modules", "vault", "services", "secrets", "service.codefly.yaml"), `kind: service
name: secrets
version: 0.0.0
agent:
    kind: runtime::service
    name: go-grpc
    version: 0.0.16
    publisher: codefly.ai
endpoints:
    - name: http
      api: http
      visibility: internal
      allow-modules: [platform]
`)
	write(t, filepath.Join(dir, "modules", "platform", "module.codefly.yaml"), "kind: module\nname: platform\nservices:\n    - name: gateway\n")
	write(t, filepath.Join(dir, "modules", "platform", "services", "gateway", "service.codefly.yaml"), consumer("gateway"))
	write(t, filepath.Join(dir, "modules", "web", "module.codefly.yaml"), "kind: module\nname: web\nservices:\n    - name: portal\n")
	write(t, filepath.Join(dir, "modules", "web", "services", "portal", "service.codefly.yaml"), consumer("portal"))
	return dir
}

func addDependency(t *testing.T, dir, module, name string, endpoints ...string) error {
	t.Helper()
	ctx := context.Background()
	ws, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	action, err := service.NewActionAddServiceDependency(ctx, &actionsv0.AddServiceDependency{
		Name:             name,
		Module:           module,
		DependencyName:   "secrets",
		DependencyModule: "vault",
		Endpoints:        endpoints,
	})
	require.NoError(t, err)
	_, err = action.Run(ctx, &actions.Space{Workspace: ws})
	return err
}

func TestAddDependencyEnforcesVisibility(t *testing.T) {
	dir := visibilityWorkspace(t)

	// The allow-listed module may depend on the internal endpoint.
	require.NoError(t, addDependency(t, dir, "platform", "gateway", "http"))

	// A module outside the allow-list is refused.
	err := addDependency(t, dir, "web", "portal", "http")
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not permit module \"web\"")
}

// TestAddDependencyEnforcesVisibilityAllEndpoints covers the unnamed-dependency
// case: consuming every endpoint must still be checked, not skipped.
func TestAddDependencyEnforcesVisibilityAllEndpoints(t *testing.T) {
	dir := visibilityWorkspace(t)

	err := addDependency(t, dir, "web", "portal")
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not permit module \"web\"")
}
