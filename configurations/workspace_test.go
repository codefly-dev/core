package configurations_test

import (
	"context"
	"os"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	actionworkspace "github.com/codefly-dev/core/actions/workspace"
	"github.com/codefly-dev/core/configurations"
	actionsv0 "github.com/codefly-dev/core/generated/go/actions/v0"
	v0base "github.com/codefly-dev/core/generated/go/base/v0"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

func createTestWorkspace(t *testing.T, ctx context.Context) (*configurations.Workspace, string) {
	tmpDir := t.TempDir()

	org := &v0base.Organization{
		Name:                 "codefly",
		SourceVersionControl: "https://github/codefly-dev",
	}

	action, err := actionworkspace.NewActionAddWorkspace(ctx, &actionsv0.AddWorkspace{
		Organization: org,
		Name:         configurations.LocalWorkspace,
		Dir:          tmpDir,
		ProjectRoot:  tmpDir,
	})
	assert.NoError(t, err)

	out, err := action.Run(ctx)
	assert.NoError(t, err)

	w := shared.Must(actions.As[configurations.Workspace](out))
	assert.Equal(t, "codefly", w.Organization.Name)
	assert.Equal(t, "https://github/codefly-dev", w.Domain)
	assert.Equal(t, configurations.LocalWorkspace, w.Name)
	assert.Equal(t, tmpDir, w.Dir())
	configurations.SetLoadWorkspaceUnsafe(w)
	return w, tmpDir
}

func TestCreateWorkspace(t *testing.T) {
	ctx := context.Background()
	workspace, dir := createTestWorkspace(t, ctx)
	defer os.RemoveAll(dir)

	workspace, err := configurations.LoadWorkspaceFromDirUnsafe(ctx, dir)
	assert.NoError(t, err)
	assert.Equal(t, "codefly", workspace.Organization.Name)
	assert.Equal(t, "https://github/codefly-dev", workspace.Domain)

	// Get active
	workspace, err = configurations.LoadWorkspace(ctx, workspace.Name)
	assert.NoError(t, err)
	assert.Equal(t, "codefly", workspace.Organization.Name)

}
