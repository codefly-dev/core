package resources

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWorkspaceDeclarationSourceRefusesAnUnresolvableRelativePath(t *testing.T) {
	// Linux refuses Getwd in an unlinked current directory. Darwin can still
	// return its former path, so this filesystem refusal is exercised on Linux.
	dir := t.TempDir()
	t.Chdir(dir)
	require.NoError(t, os.Remove(dir))
	workspace := &Workspace{}
	require.Error(t, workspace.setDeclarationSource("workspace.codefly.yaml", []byte("name: product\n")))
	file, digest := workspace.DeclarationSource()
	require.Empty(t, file)
	require.Empty(t, digest)
}
