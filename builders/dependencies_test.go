package builders_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

func TestHash(t *testing.T) {
	ctx := context.Background()
	// create a temporary directory
	d, err := os.MkdirTemp("", "example")
	assert.NoError(t, err)
	defer os.RemoveAll(d)

	dep := builders.NewDependency("test", d)
	dep.WithDir(d)

	updated, err := dep.Updated(ctx)
	assert.NoError(t, err)
	assert.True(t, updated)

	// create a file inside the temporary directory
	f, err := os.CreateTemp(d, "tmp")
	_, err = f.Write([]byte("hello world"))
	assert.NoError(t, err)
	err = f.Close()
	assert.NoError(t, err)

	updated, err = dep.Updated(ctx)
	assert.NoError(t, err)
	assert.True(t, updated)

	// To write to the file, you need to open it with write access
	f, err = os.OpenFile(f.Name(), os.O_APPEND|os.O_WRONLY, 0600)
	assert.NoError(t, err)

	_, err = f.Write([]byte("hello again"))
	assert.NoError(t, err)

	err = f.Close()
	assert.NoError(t, err)

	updated, err = dep.Updated(ctx)
	assert.NoError(t, err)
	assert.True(t, updated)

}

func TestHashFolderAndFilter(t *testing.T) {
	// create a temporary directory
	d, err := os.MkdirTemp("", "example")
	assert.NoError(t, err)
	ctx := context.Background()
	defer os.RemoveAll(d)

	dep := builders.NewDependency("test", d)
	dep.WithIgnore(shared.NewIgnore("*.md")).WithDir(d)

	updated, err := dep.Updated(ctx)
	assert.NoError(t, err)
	assert.True(t, updated)

	dir := filepath.Join(d, "dir")
	err = os.Mkdir(dir, 0755)
	assert.NoError(t, err)

	updated, err = dep.Updated(ctx)
	assert.NoError(t, err)
	assert.False(t, updated)

	// Add an ignored file
	f, err := os.Create(filepath.Join(dir, "tmp.md"))
	assert.NoError(t, err)
	_, err = f.Write([]byte("hello world"))
	assert.NoError(t, err)
	err = f.Close()
	assert.NoError(t, err)

	updated, err = dep.Updated(ctx)
	assert.NoError(t, err)
	assert.False(t, updated)

	// Add a non-ignored file
	f, err = os.CreateTemp(dir, "tmp.txt")
	assert.NoError(t, err)
	_, err = f.Write([]byte("hello world"))
	assert.NoError(t, err)
	err = f.Close()
	assert.NoError(t, err)

	updated, err = dep.Updated(ctx)
	assert.NoError(t, err)
	assert.True(t, updated)
}
