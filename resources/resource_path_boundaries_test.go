package resources

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	actionsv0 "github.com/codefly-dev/core/generated/go/codefly/actions/v0"
)

func TestCreateWorkspaceRejectsTraversingNameBeforeCreatingDirectory(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	escaped := filepath.Join(filepath.Dir(root), "escaped-workspace")
	_ = os.RemoveAll(escaped)

	_, err := CreateWorkspace(ctx, &actionsv0.NewWorkspace{
		Name:   "../escaped-workspace",
		Path:   root,
		Layout: LayoutKindModules,
	})
	if err == nil {
		t.Fatal("traversing workspace name was accepted")
	}
	if _, statErr := os.Stat(escaped); !os.IsNotExist(statErr) {
		t.Fatalf("escaped workspace path was created: %v", statErr)
	}
}

func TestResourceCreatorsRejectTraversingNames(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	workspace := &Workspace{Name: "workspace", Layout: LayoutKindModules, dir: root}
	layout, err := NewLayout(ctx, root, LayoutKindModules, nil)
	if err != nil {
		t.Fatal(err)
	}
	workspace.layout = layout

	if _, err := workspace.NewModule(ctx, &actionsv0.NewModule{Name: "../escaped-module"}); err == nil {
		t.Fatal("traversing module name was accepted")
	}
	mod := &Module{Name: "module", dir: root}
	if _, err := mod.NewService(ctx, &actionsv0.AddService{Name: "../escaped-service"}); err == nil {
		t.Fatal("traversing service name was accepted")
	}
	if _, err := mod.NewApplication(ctx, &actionsv0.AddApplication{Name: "../escaped-application"}); err == nil {
		t.Fatal("traversing application name was accepted")
	}
}

func TestLoadedResourcesRejectUnsafeNamesAndRelativeOverrides(t *testing.T) {
	ctx := context.Background()

	t.Run("module reference", func(t *testing.T) {
		dir := t.TempDir()
		content := []byte("kind: module\nname: module\nservices:\n  - name: ../../escape\n")
		if err := os.WriteFile(filepath.Join(dir, ModuleConfigurationName), content, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadModuleFromDir(ctx, dir); err == nil {
			t.Fatal("unsafe service reference was accepted")
		}
	})

	t.Run("relative override", func(t *testing.T) {
		dir := t.TempDir()
		content := []byte("kind: module\nname: module\nservices:\n  - name: service\n    path: ../../escape\n")
		if err := os.WriteFile(filepath.Join(dir, ModuleConfigurationName), content, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadModuleFromDir(ctx, dir); err == nil {
			t.Fatal("traversing relative override was accepted")
		}
	})

	t.Run("service", func(t *testing.T) {
		dir := t.TempDir()
		content := []byte("name: ../escape\nversion: 0.0.1\n")
		if err := os.WriteFile(filepath.Join(dir, ServiceConfigurationName), content, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadServiceFromDir(ctx, dir); err == nil {
			t.Fatal("unsafe service name was accepted")
		}
	})

	t.Run("application", func(t *testing.T) {
		dir := t.TempDir()
		content := []byte("kind: application\nname: ../escape\nversion: 0.0.1\n")
		if err := os.WriteFile(filepath.Join(dir, ApplicationConfigurationName), content, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadApplicationFromDir(ctx, dir); err == nil {
			t.Fatal("unsafe application name was accepted")
		}
	})
}

func TestAbsoluteResourceOverridesRemainSupported(t *testing.T) {
	abs := t.TempDir()
	if err := validateResourcePathOverride("service", &abs); err != nil {
		t.Fatalf("absolute override rejected: %v", err)
	}
}
