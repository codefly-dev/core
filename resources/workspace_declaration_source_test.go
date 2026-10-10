package resources_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestWorkspaceDeclarationSourceUsesParsedBytes(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "workspace.codefly.yaml")
	original := []byte("name: original\nlayout: modules\n")
	require.NoError(t, os.WriteFile(file, original, 0600))
	workspace, err := resources.LoadWorkspaceFromDir(context.Background(), dir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(file, []byte("name: changed\nlayout: modules\n"), 0600))
	actualPath, digest := workspace.DeclarationSource()
	require.Equal(t, file, actualPath)
	require.Equal(t, fmt.Sprintf("%x", sha256.Sum256(original)), digest)
	require.Equal(t, "original", workspace.Name)
	encoded, err := yaml.Marshal(workspace)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), digest)
	empty := &resources.Workspace{}
	emptyPath, emptyDigest := empty.DeclarationSource()
	require.Empty(t, emptyPath)
	require.Empty(t, emptyDigest)
}
