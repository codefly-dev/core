package resources_test

//
//import (
//	"context"
//	"os"
//	"path/filepath"
//	"testing"
//
//	"github.com/codefly-dev/core/actions/actions"
//	actionmodule "github.com/codefly-dev/core/actions/module"
//	actionsv0 "github.com/codefly-dev/core/generated/go/codefly/actions/v0"
//	"github.com/codefly-dev/core/resources"
//	"github.com/codefly-dev/core/shared"
//	"github.com/stretchr/testify/assert"
//)
//
//func TestCreationModule(t *testing.T) {
//	tmpDir := t.TempDir()
//
//	defer func() {
//		os.RemoveAll(tmpDir)
//	}()
//	ctx := context.Background()
//
//	var action actions.Action
//	var err error
//
//	action, err = action.NewActionNe(ctx, &actionsv0.New{
//		Name: "test-",
//		Path: tmpDir,
//	})
//require.NoError(t, err)
//	out, err := action.Run(ctx)
//require.NoError(t, err)
//	 := shared.Must(actions.As[resources.](out))
//
//	action, err = actionmodule.NewActionAddModule(ctx, &actionsv0.NewModule{
//		Name:        "test-module",
//		Path: .Dir(),
//	})
//require.NoError(t, err)
//	out, err = action.Run(ctx)
//require.NoError(t, err)
//
//	app, err := actions.As[resources.Module](out)
//require.NoError(t, err)
//	require.Equal(t, "test-module", app.Name)
//
//	// Check that there is a configuration file
//	_, err = os.Stat(filepath.Join(.Dir(), "modules/test-module", resources.ModuleConfigurationName))
//
//	// Run again should produce error
//	_, err = action.Run(ctx)
//	require.Error(t, err)
//
//	// Re-load the
//	, err = resources.Reload(ctx, )
//require.NoError(t, err)
//	require.Equal(t, 1, len(.Modules))
//
//	// Check that we have the configuration
//	back, err := .LoadModuleFromName(ctx, "test-module")
//require.NoError(t, err)
//	require.Equal(t, app.Name, back.Name)
//
//	// Add a second module
//	action, err = actionmodule.NewActionAddModule(ctx, &actionsv0.NewModule{
//		Name:        "test-module-2",
//		Path: .Dir(),
//	})
//require.NoError(t, err)
//	out, err = action.Run(ctx)
//require.NoError(t, err)
//	app, err = actions.As[resources.Module](out)
//require.NoError(t, err)
//	require.Equal(t, "test-module-2", app.Name)
//
//	, err = resources.Reload(ctx, )
//require.NoError(t, err)
//
//	require.Equal(t, 2, len(.Modules))
//}
//
//func TestCreationModuleWithWorkspace(t *testing.T) {
//	ctx := context.Background()
//	workspace, dir := createTestWorkspace(t, ctx)
//	cur, err := os.Getwd()
//require.NoError(t, err)
//	err = os.Chdir(dir)
//require.NoError(t, err)
//
//	defer func() {
//		os.RemoveAll(dir)
//		os.Chdir(cur)
//	}()
//
//	var action actions.Action
//	action, err = action.NewActionNew(ctx, &actionsv0.New{
//		Name: "test-",
//		Path: workspace.Dir(),
//	})
//require.NoError(t, err)
//	out, err := action.Run(ctx)
//require.NoError(t, err)
//	 := shared.Must(actions.As[resources.](out))
//
//	// Add to workspace
//	action, err = action.NewActionAddToWorkspace(ctx, &actionsv0.AddToWorkspace{
//		Name:      .Name,
//		Workspace: workspace.Name,
//		Path:      .Dir(),
//	})
//require.NoError(t, err)
//	out, err = action.Run(ctx)
//require.NoError(t, err)
//
//	workspace = shared.Must(actions.As[resources.Workspace](out))
//
//	require.Equal(t, "test-", workspace.Active)
//
//	action, err = actionmodule.NewActionAddModule(ctx, &actionsv0.NewModule{
//		Name:        "test-module",
//		Path: .Dir(),
//	})
//require.NoError(t, err)
//	out, err = action.Run(ctx)
//require.NoError(t, err)
//
//	app := shared.Must(actions.As[resources.Module](out))
//	require.Equal(t, "test-module", app.Name)
//	require.Equal(t, .Name, app.)
//	require.Equal(t, filepath.Join(.Dir(), "modules/test-module"), app.Dir())
//
//	// Running again should produce an error
//	_, err = action.Run(ctx)
//	require.Error(t, err)
//
//	// Add app to workspace
//	action, err = actionmodule.NewActionAddModuleToWorkspace(ctx, &actionsv0.AddModuleToWorkspace{
//		Name:      app.Name,
//		:   .Name,
//		Workspace: workspace.Name,
//	})
//require.NoError(t, err)
//	out, err = action.Run(ctx)
//require.NoError(t, err)
//	workspace = shared.Must(actions.As[resources.Workspace](out))
//
//	, err = resources.Reload(ctx, )
//require.NoError(t, err)
//
//	// Check that we have the app
//	back, err := .LoadModuleFromName(ctx, "test-module")
//require.NoError(t, err)
//	require.Equal(t, app.Name, back.Name)
//
//	// One app should be active
//
//	// Check the active module
//	back, err = workspace.LoadActiveModule(ctx, .Name)
//require.NoError(t, err)
//	require.Equal(t, app.Name, back.Name)
//
//	// Add a second module
//	action, err = actionmodule.NewActionAddModule(ctx, &actionsv0.NewModule{
//		Name:        "test-module-2",
//		Path: .Name,
//	})
//require.NoError(t, err)
//	out, err = action.Run(ctx)
//require.NoError(t, err)
//	app, err = actions.As[resources.Module](out)
//require.NoError(t, err)
//	require.Equal(t, "test-module-2", app.Name)
//
//	, err = resources.Reload(ctx, )
//require.NoError(t, err)
//
//	require.Equal(t, 2, len(.Modules))
//
//	// Add workspace
//	action, err = actionmodule.NewActionAddModuleToWorkspace(ctx, &actionsv0.AddModuleToWorkspace{
//		Name:      app.Name,
//		:   .Name,
//		Workspace: workspace.Name,
//	})
//require.NoError(t, err)
//	out, err = action.Run(ctx)
//require.NoError(t, err)
//	workspace = shared.Must(actions.As[resources.Workspace](out))
//
//	// Set active
//	action, err = actionmodule.NewActionSetModuleActive(ctx, &actionsv0.SetModuleActive{
//		Name:      app.Name,
//		:   .Name,
//		Workspace: workspace.Name,
//	})
//require.NoError(t, err)
//	out, err = action.Run(ctx)
//require.NoError(t, err)
//	workspace = shared.Must(actions.As[resources.Workspace](out))
//
//	// Check active is the second one
//	back, err = workspace.LoadActiveModule(ctx, .Name)
//
//	// Make the first one active
//	action, err = actionmodule.NewActionSetModuleActive(ctx, &actionsv0.SetModuleActive{
//		Name:      "test-module",
//		:   .Name,
//		Workspace: workspace.Name,
//	})
//require.NoError(t, err)
//	out, err = action.Run(ctx)
//require.NoError(t, err)
//
//	workspace, err = actions.As[resources.Workspace](out)
//require.NoError(t, err)
//	active, err := workspace.LoadActiveModule(ctx, .Name)
//	require.Equal(t, "test-module", active.Name)
//
//	action, err = actionmodule.NewActionSetModuleActive(ctx, &actionsv0.SetModuleActive{
//		Name:      "test-module-2",
//		:   .Name,
//		Workspace: workspace.Name,
//	})
//require.NoError(t, err)
//	out, err = action.Run(ctx)
//require.NoError(t, err)
//	workspace, err = actions.As[resources.Workspace](out)
//require.NoError(t, err)
//
//	active, err = workspace.LoadActiveModule(ctx, .Name)
//	require.Equal(t, "test-module-2", active.Name)
//}
