package resources

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	actionsv0 "github.com/codefly-dev/core/generated/go/codefly/actions/v0"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

func testResourceAgent(kind basev0.Agent_Kind) *basev0.Agent {
	return &basev0.Agent{
		Kind:      kind,
		Name:      "test-agent",
		Publisher: "codefly.dev",
		Version:   "0.0.1",
	}
}

func TestCreateWorkspaceRemovesPartialDirectoryOnFailure(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workspaceDir := filepath.Join(root, "workspace")

	_, err := CreateWorkspace(ctx, &actionsv0.NewWorkspace{
		Name:   "workspace",
		Path:   root,
		Layout: "invalid-layout",
	})
	if err == nil {
		t.Fatal("invalid workspace layout returned success")
	}
	if _, statErr := os.Stat(workspaceDir); !os.IsNotExist(statErr) {
		t.Fatalf("partial workspace directory remains: %v", statErr)
	}
}

func TestNewModuleDoesNotPersistReferenceForExistingDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	layout, err := NewLayout(ctx, root, LayoutKindModules, nil)
	if err != nil {
		t.Fatal(err)
	}
	workspace := &Workspace{Name: "workspace", Layout: LayoutKindModules, dir: root, layout: layout}
	if err := workspace.Save(ctx); err != nil {
		t.Fatal(err)
	}
	moduleDir := filepath.Join(root, "modules", "module")
	if err := os.MkdirAll(moduleDir, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(moduleDir, "keep.txt")
	if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := workspace.NewModule(ctx, &actionsv0.NewModule{Name: "module"}); err == nil {
		t.Fatal("existing module directory returned success")
	}
	if len(workspace.Modules) != 0 {
		t.Fatalf("in-memory module reference was added: %#v", workspace.Modules)
	}
	reloaded, err := LoadWorkspaceFromDir(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Modules) != 0 {
		t.Fatalf("persisted module reference was added: %#v", reloaded.Modules)
	}
	if content, err := os.ReadFile(marker); err != nil || string(content) != "keep" {
		t.Fatalf("existing module directory was modified: content=%q err=%v", content, err)
	}
}

func TestNewModuleRollsBackWhenWorkspaceSaveFails(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	layout, err := NewLayout(ctx, root, LayoutKindModules, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, WorkspaceConfigurationName), 0o700); err != nil {
		t.Fatal(err)
	}
	workspace := &Workspace{Name: "workspace", Layout: LayoutKindModules, dir: root, layout: layout}

	if _, err := workspace.NewModule(ctx, &actionsv0.NewModule{Name: "module"}); err == nil {
		t.Fatal("workspace save conflict returned success")
	}
	if len(workspace.Modules) != 0 {
		t.Fatalf("module references were not restored: %#v", workspace.Modules)
	}
	if _, statErr := os.Stat(filepath.Join(root, "modules", "module")); !os.IsNotExist(statErr) {
		t.Fatalf("partial module directory remains: %v", statErr)
	}
}

func TestNewServiceRollsBackDirectoryAndReferenceWhenModuleSaveFails(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ModuleConfigurationName), 0o700); err != nil {
		t.Fatal(err)
	}
	mod := &Module{Kind: ModuleKind, Name: "module", dir: root}

	_, err := mod.NewService(ctx, &actionsv0.AddService{
		Name:  "service",
		Agent: testResourceAgent(basev0.Agent_SERVICE),
	})
	if err == nil {
		t.Fatal("module save conflict returned success")
	}
	if len(mod.ServiceReferences) != 0 {
		t.Fatalf("service references were not restored: %#v", mod.ServiceReferences)
	}
	if _, statErr := os.Stat(filepath.Join(root, "services", "service")); !os.IsNotExist(statErr) {
		t.Fatalf("partial service directory remains: %v", statErr)
	}
}

func TestNewApplicationRollsBackDirectoryAndReferenceWhenModuleSaveFails(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ModuleConfigurationName), 0o700); err != nil {
		t.Fatal(err)
	}
	mod := &Module{Kind: ModuleKind, Name: "module", dir: root}

	_, err := mod.NewApplication(ctx, &actionsv0.AddApplication{
		Name:  "application",
		Agent: testResourceAgent(basev0.Agent_APPLICATION),
	})
	if err == nil {
		t.Fatal("module save conflict returned success")
	}
	if len(mod.ApplicationReferences) != 0 {
		t.Fatalf("application references were not restored: %#v", mod.ApplicationReferences)
	}
	if _, statErr := os.Stat(filepath.Join(root, "applications", "application")); !os.IsNotExist(statErr) {
		t.Fatalf("partial application directory remains: %v", statErr)
	}
}

func TestNewResourcesDoNotClobberUnreferencedDirectories(t *testing.T) {
	ctx := context.Background()
	for name, test := range map[string]struct {
		dir string
		run func(*Module) error
	}{
		"service": {
			dir: filepath.Join("services", "service"),
			run: func(mod *Module) error {
				_, err := mod.NewService(ctx, &actionsv0.AddService{
					Name:  "service",
					Agent: testResourceAgent(basev0.Agent_SERVICE),
				})
				return err
			},
		},
		"application": {
			dir: filepath.Join("applications", "application"),
			run: func(mod *Module) error {
				_, err := mod.NewApplication(ctx, &actionsv0.AddApplication{
					Name:  "application",
					Agent: testResourceAgent(basev0.Agent_APPLICATION),
				})
				return err
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			resourceDir := filepath.Join(root, test.dir)
			if err := os.MkdirAll(resourceDir, 0o700); err != nil {
				t.Fatal(err)
			}
			marker := filepath.Join(resourceDir, "keep.txt")
			if err := os.WriteFile(marker, []byte("keep"), 0o600); err != nil {
				t.Fatal(err)
			}

			mod := &Module{Kind: ModuleKind, Name: "module", dir: root}
			if err := test.run(mod); err == nil {
				t.Fatal("unreferenced existing directory was accepted")
			}
			if content, err := os.ReadFile(marker); err != nil || string(content) != "keep" {
				t.Fatalf("existing directory was modified: content=%q err=%v", content, err)
			}
		})
	}
}
