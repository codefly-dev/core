package builders_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

func TestHash(t *testing.T) {
	// create a temporary directory
	d, err := os.MkdirTemp("", "example")
	assert.NoError(t, err)
	defer os.RemoveAll(d)

	dep := &builders.Dependency{Components: []string{d}}
	h, err := dep.Hash()
	assert.NoError(t, err)

	// create a file inside the temporary directory
	f, err := os.CreateTemp(d, "tmp")
	_, err = f.Write([]byte("hello world"))
	assert.NoError(t, err)
	err = f.Close()
	assert.NoError(t, err)
	h2, err := dep.Hash()
	assert.NoError(t, err)

	assert.NotEqual(t, h, h2)

	// To write to the file, you need to open it with write access
	f, err = os.OpenFile(f.Name(), os.O_APPEND|os.O_WRONLY, 0600)
	assert.NoError(t, err)

	_, err = f.Write([]byte("hello again"))
	assert.NoError(t, err)

	err = f.Close()
	assert.NoError(t, err)

	h3, err := dep.Hash()
	assert.NoError(t, err)
	assert.NotEqual(t, h2, h3)

}

func TestHashFolderAndFilter(t *testing.T) {
	// create a temporary directory
	d, err := os.MkdirTemp("", "example")
	assert.NoError(t, err)
	defer os.RemoveAll(d)

	dep := &builders.Dependency{Components: []string{d}, Ignore: shared.NewIgnore("*.md")}
	h, err := dep.Hash()
	assert.NoError(t, err)

	dir := filepath.Join(d, "dir")
	err = os.Mkdir(dir, 0755)
	assert.NoError(t, err)
	h2, err := dep.Hash()
	assert.NoError(t, err)
	assert.Equal(t, h, h2)

	// Add an ignored file
	f, err := os.Create(filepath.Join(dir, "tmp.md"))
	assert.NoError(t, err)
	_, err = f.Write([]byte("hello world"))
	assert.NoError(t, err)
	err = f.Close()
	assert.NoError(t, err)

	h3, err := dep.Hash()
	assert.NoError(t, err)
	assert.Equal(t, h, h3)

	// Add a non-ignored file
	f, err = os.CreateTemp(dir, "tmp.txt")
	assert.NoError(t, err)
	_, err = f.Write([]byte("hello world"))
	assert.NoError(t, err)
	err = f.Close()
	assert.NoError(t, err)

	h4, err := dep.Hash()
	assert.NoError(t, err)
	assert.NotEqual(t, h, h4)

}
