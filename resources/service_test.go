package resources_test

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestLoadingFromPathFlat(t *testing.T) {
	testLoadingFromPath(t, "testdata/workspaces/flat-layout")
}

func TestLoadingFromPathModule(t *testing.T) {
	testLoadingFromPath(t, "testdata/workspaces/module-layout")
}

func testLoadingFromPath(t *testing.T, dir string) {
	ctx := context.Background()

	cur, err := os.Getwd()
	require.NoError(t, err)

	err = os.Chdir(path.Join(cur, dir))
	require.NoError(t, err)
	defer os.Chdir(cur)

	workspace, err := resources.FindWorkspaceUp(ctx)
	require.NoError(t, err)
	require.Equal(t, "codefly-platform", workspace.Name)
	require.Equal(t, 1, len(workspace.Modules))

	// Save and make sure we preserve the "active module" convention
	tmpDir := t.TempDir()

	err = workspace.SaveToDirUnsafe(ctx, tmpDir)
	require.NoError(t, err)

	_, err = os.ReadFile(path.Join(tmpDir, resources.WorkspaceConfigurationName))
	require.NoError(t, err)
}

func TestServiceUnique(t *testing.T) {
	svc := resources.Service{Name: "svc", Module: "app"}
	unique := svc.Unique()
	inf, err := resources.ParseServiceWithOptionalModule(unique)
	require.NoError(t, err)
	require.Equal(t, "svc", inf.Name)
	require.Equal(t, "app", inf.Module)
}

type testServiceSpec struct {
	TestField string `yaml:"test-field"`
}

func TestSpecSave(t *testing.T) {
	ctx := context.Background()
	s := &resources.Service{Name: "testName"}
	out, err := yaml.Marshal(s)
	require.NoError(t, err)
	require.Contains(t, string(out), "testName")

	err = s.UpdateSpecFromSettings(&testServiceSpec{TestField: "testKind"})
	require.NoError(t, err)
	require.NotNilf(t, s.Spec, "UpdateSpecFromSettings failed")

	_, ok := s.Spec["test-field"]
	require.True(t, ok)

	// save to file
	tmp := t.TempDir()
	err = s.SaveAtDir(ctx, tmp)
	require.NoError(t, err)

	// make sure it looks good
	content, err := os.ReadFile(path.Join(tmp, resources.ServiceConfigurationName))
	require.NoError(t, err)
	require.Contains(t, string(content), "test-field")
	require.Contains(t, string(content), "testKind")

	s, err = resources.LoadFromDir[resources.Service](ctx, tmp)
	require.NoError(t, err)

	require.NoError(t, err)
	var field testServiceSpec
	err = s.LoadSettingsFromSpec(&field)
	require.NoError(t, err)
	require.Equal(t, "testKind", field.TestField)
}
