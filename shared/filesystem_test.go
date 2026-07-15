package shared_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

func TestFSReaderCopyResolvesRootOnce(t *testing.T) {
	embedded := fstest.MapFS{
		"templates/config.txt": &fstest.MapFile{
			Data: []byte("configuration"),
			Mode: fs.FileMode(0600),
		},
	}
	destination := filepath.Join(t.TempDir(), "config.txt")

	err := shared.Embed(embedded).At("templates").Copy("config.txt", destination)

	require.NoError(t, err)
	content, err := os.ReadFile(destination)
	require.NoError(t, err)
	require.Equal(t, "configuration", string(content))
}
