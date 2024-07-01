package test

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/actions/service"

	"github.com/codefly-dev/core/actions/module"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/actions/workspace"

	"github.com/codefly-dev/core/actions/actions"
	actionsv0 "github.com/codefly-dev/core/generated/go/codefly/actions/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestWorkspaceFlatLayout(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)

	ctx := context.Background()

	p := t.TempDir()
	//p = shared.MustSolvePath("testdata")

	defer func() {
		err := os.RemoveAll(p)
		require.NoError(t, err)
	}()

	var action actions.Action
	var err error

	Name := "test-flat-workspace"
	action, err = workspace.NewActionNewWorkspace(ctx, &actionsv0.NewWorkspace{
		Name:   Name,
		Path:   p,
		Layout: resources.LayoutKindFlat,
	})
	require.NoError(t, err)

	out, err := action.Run(ctx, nil)
	require.NoError(t, err)

	ws, err := actions.As[resources.Workspace](out)
	require.NoError(t, err)
	require.Equal(t, Name, ws.Name)
	require.Equal(t, path.Join(p, Name), ws.Dir())

	// Creating again should return an error
	_, err = action.Run(ctx, nil)
	require.Error(t, err)

	// Check that we have these files at root
	files := []string{resources.WorkspaceConfigurationName, resources.ModuleConfigurationName, "README.md"}
	for _, file := range files {
		_, err = shared.FileExists(ctx, path.Join(ws.Dir(), file))
		require.NoError(t, err)
	}

	// Read the workspace configuration
	// Ensure we don't save "modules"
	content, err := os.ReadFile(path.Join(ws.Dir(), resources.WorkspaceConfigurationName))
	require.NoError(t, err)
	require.NotContains(t, string(content), "modules:")

	// Check that we have these folders at root
	folders := []string{"services", "configurations", "configurations/local"}
	for _, folder := range folders {
		_, err = shared.DirectoryExists(ctx, path.Join(ws.Dir(), folder))
		require.NoError(t, err)

	}
	files = []string{"services/README.md"}
	for _, file := range files {
		_, err = shared.FileExists(ctx, path.Join(ws.Dir(), file))
		require.NoError(t, err)
	}

	files = []string{"configurations/README.md"}
	for _, file := range files {
		_, err = shared.FileExists(ctx, path.Join(ws.Dir(), file))
		require.NoError(t, err)
	}

	// Create a new Module should get an error
	action, err = module.NewActionAddModule(ctx, &actionsv0.NewModule{
		Name: "should get error",
	})
	require.NoError(t, err)
	out, err = action.Run(ctx, &actions.Space{Workspace: ws})
	require.Error(t, err)

	// Get the root module
	mod, err := ws.RootModule(ctx)
	require.NoError(t, err)

	// Create a Service
	action, err = service.NewActionAddService(ctx, &actionsv0.AddService{
		Name:  "service-test",
		Agent: agentTest(),
	})
	require.NoError(t, err)

	out, err = action.Run(ctx, &actions.Space{Module: mod})
	require.NoError(t, err)

	// Check files
	files = []string{resources.ServiceConfigurationName, "README.md"}
	for _, file := range files {
		_, err = shared.FileExists(ctx, path.Join(ws.Dir(), "services", "service-test", file))
		require.NoError(t, err)
	}

	// Check that we have these folders in service
	folders = []string{"configurations", "configurations/local"}
	for _, folder := range folders {
		_, err = shared.DirectoryExists(ctx, path.Join(ws.Dir(), "services", "service-test", folder))
		require.NoError(t, err)
	}

	svc, err := actions.As[resources.Service](out)
	require.NoError(t, err)
	require.Equal(t, "service-test", svc.Name)
}

func TestModuleLayout(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)

	ctx := context.Background()

	p := t.TempDir()
	// p = shared.MustSolvePath("testdata")

	defer func() {
		err := os.RemoveAll(p)
		require.NoError(t, err)
	}()

	var action actions.Action
	var err error

	Name := "test-flat-workspace"
	action, err = workspace.NewActionNewWorkspace(ctx, &actionsv0.NewWorkspace{
		Name:   Name,
		Path:   p,
		Layout: resources.LayoutKindModules,
	})
	require.NoError(t, err)

	out, err := action.Run(ctx, nil)
	require.NoError(t, err)

	ws, err := actions.As[resources.Workspace](out)
	require.NoError(t, err)
	require.Equal(t, Name, ws.Name)
	require.Equal(t, path.Join(p, Name), ws.Dir())

	// Creating again should return an error
	out, err = action.Run(ctx, nil)
	require.Error(t, err)

	// Check that we have these files at root
	files := []string{resources.WorkspaceConfigurationName, "README.md"}
	for _, file := range files {
		_, err = shared.FileExists(ctx, path.Join(ws.Dir(), file))
		require.NoError(t, err)
	}

	// Check that we have these folders at root
	folders := []string{"modules", "configurations", "configurations/local"}
	for _, folder := range folders {
		_, err = shared.DirectoryExists(ctx, path.Join(ws.Dir(), folder))
		require.NoError(t, err)

	}
	files = []string{"modules/README.md"}
	for _, file := range files {
		_, err = shared.FileExists(ctx, path.Join(ws.Dir(), file))
		require.NoError(t, err)
	}

	files = []string{"configurations/README.md"}
	for _, file := range files {
		_, err = shared.FileExists(ctx, path.Join(ws.Dir(), file))
		require.NoError(t, err)
	}

	// Create a new Module should get an error
	action, err = module.NewActionAddModule(ctx, &actionsv0.NewModule{
		Name: "module-test",
	})
	require.NoError(t, err)

	out, err = action.Run(ctx, &actions.Space{Workspace: ws})
	require.NoError(t, err)

	mod, err := actions.As[resources.Module](out)
	require.NoError(t, err)
	require.Equal(t, "module-test", mod.Name)
	require.Equal(t, path.Join(ws.Dir(), "modules", "module-test"), mod.Dir())

	// Check that we have these folders in module
	folders = []string{"configurations", "configurations/local", "services"}
	for _, folder := range folders {
		_, err = shared.DirectoryExists(ctx, path.Join(ws.Dir(), "modules", "module-test", folder))
		require.NoError(t, err)
	}

	// Get the root module
	_, err = ws.RootModule(ctx)
	require.Error(t, err)

	// Create a Service
	action, err = service.NewActionAddService(ctx, &actionsv0.AddService{
		Name:  "service-test",
		Agent: agentTest(),
	})
	require.NoError(t, err)

	out, err = action.Run(ctx, &actions.Space{Module: mod})
	require.NoError(t, err)

	// Check files
	files = []string{resources.ServiceConfigurationName, "README.md"}
	for _, file := range files {
		_, err = shared.FileExists(ctx, path.Join(ws.Dir(), "modules", "module-test", "services", "service-test", file))
		require.NoError(t, err)
	}

	// Check that we have these folders in service
	folders = []string{"configurations", "configurations/local"}
	for _, folder := range folders {
		_, err = shared.DirectoryExists(ctx, path.Join(ws.Dir(), "modules", "module-test", "services", "service-test", folder))
		require.NoError(t, err)
	}

	svc, err := actions.As[resources.Service](out)
	require.NoError(t, err)
	require.Equal(t, "service-test", svc.Name)

}
