package configurations_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/actions/actions"
	actionworkspace "github.com/codefly-dev/core/actions/workspace"
	"github.com/codefly-dev/core/configurations"
	v1actions "github.com/codefly-dev/core/proto/v1/go/actions"
	v1base "github.com/codefly-dev/core/proto/v1/go/base"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

func createTestWorkspace(t *testing.T, ctx context.Context) (*configurations.Workspace, string) {
	tmpDir := t.TempDir()

	org := &v1base.Organization{
		Name:   "codefly",
		Domain: "https://github/codefly-dev",
	}

	action := actionworkspace.NewAddWorkspaceAction(&v1actions.AddWorkspace{
		Organization: org,
		Name:         "test",
		Dir:          tmpDir,
	})

	out, err := action.Run(ctx)
	assert.NoError(t, err)

	w := shared.Must(actions.As[configurations.Workspace](out))
	assert.Equal(t, "codefly", w.Organization)
	assert.Equal(t, "https://github/codefly-dev", w.Domain)
	assert.Equal(t, "test", w.Name)
	assert.Equal(t, tmpDir, w.Dir())
	return w, tmpDir
}

func TestCreateWorkspace(t *testing.T) {
	ctx := shared.NewContext()
	w, dir := createTestWorkspace(t, ctx)

	// Save
	err := w.Save(ctx)
	assert.NoError(t, err)

	// Load back
	w, err = configurations.LoadWorkspaceFromDir(ctx, dir)
	assert.NoError(t, err)
	assert.Equal(t, "codefly", w.Organization)
	assert.Equal(t, "https://github/codefly-dev", w.Domain)

	// Do a reset
	configurations.Reset()

	// Get current
	w, err = configurations.CurrentWorkspace(ctx)
	assert.NoError(t, err)
	assert.Equal(t, "codefly", w.Organization)

}
