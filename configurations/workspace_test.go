package configurations_test

import (
	"context"
	"os"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	actionworkspace "github.com/codefly-dev/core/actions/workspace"
	"github.com/codefly-dev/core/configurations"
	actionsv1 "github.com/codefly-dev/core/generated/go/actions/v1"
	v1base "github.com/codefly-dev/core/generated/go/base/v1"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

func createTestWorkspace(t *testing.T, ctx context.Context) (*configurations.Workspace, string) {
	tmpDir := t.TempDir()

	org := &v1base.Organization{
		Name:   "codefly",
		Domain: "https://github/codefly-dev",
	}

	action, err := actionworkspace.NewActionAddWorkspace(ctx, &actionsv1.AddWorkspace{
		Organization: org,
		Name:         "test",
		Dir:          tmpDir,
		ProjectRoot:  tmpDir,
	})
	assert.NoError(t, err)

	out, err := action.Run(ctx)
	assert.NoError(t, err)

	w := shared.Must(actions.As[configurations.Workspace](out))
	assert.Equal(t, "codefly", w.Organization.Name)
	assert.Equal(t, "https://github/codefly-dev", w.Domain)
	assert.Equal(t, "test", w.Name)
	assert.Equal(t, tmpDir, w.Dir())
	configurations.SetLoadWorkspaceUnsafe(w)
	return w, tmpDir
}

func TestCreateWorkspace(t *testing.T) {
	ctx := context.Background()
	w, dir := createTestWorkspace(t, ctx)
	defer os.RemoveAll(dir)

	// Load back
	w, err := configurations.LoadWorkspaceFromDirUnsafe(ctx, dir)
	assert.NoError(t, err)
	assert.Equal(t, "codefly", w.Organization.Name)
	assert.Equal(t, "https://github/codefly-dev", w.Domain)

	// Get active
	w, err = configurations.LoadWorkspace(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "codefly", w.Organization.Name)

}
