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

func TestHashMissingFile(t *testing.T) {
	ctx := context.Background()
	// create a temporary directory
	d, err := os.MkdirTemp("", "example")
	assert.NoError(t, err)
	defer os.RemoveAll(d)

	dep := builders.NewDependencies("test", builders.NewDependency("nothin"))
	dep.Localize(d)

	_, err = dep.Updated(ctx)
	assert.NoError(t, err)
}

func TestHash(t *testing.T) {
	ctx := context.Background()
	// create a temporary directory
	d, err := os.MkdirTemp("", "example")
	assert.NoError(t, err)
	defer os.RemoveAll(d)

	dep := builders.NewDependencies("test", builders.NewDependency(d))
	dep.Localize(d)

	updated, err := dep.Updated(ctx)
	assert.NoError(t, err)
	assert.True(t, updated)
	err = dep.UpdateCache(ctx)
	assert.NoError(t, err)

	// create a file inside the temporary directory
	f, err := os.CreateTemp(d, "tmp")
	_, err = f.Write([]byte("hello world"))
	assert.NoError(t, err)
	err = f.Close()
	assert.NoError(t, err)

	updated, err = dep.Updated(ctx)
	assert.NoError(t, err)
	assert.True(t, updated)
	err = dep.UpdateCache(ctx)
	assert.NoError(t, err)

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

func TestHashWildCardSelect(t *testing.T) {
	// create a temporary directory
	d, err := os.MkdirTemp("", "example")
	assert.NoError(t, err)
	ctx := context.Background()
	defer os.RemoveAll(d)

	dep := builders.NewDependencies("test",
		builders.NewDependency(d).WithPathSelect(shared.NewSelect("*.md")))
	dep.Localize(d)

	updated, err := dep.Updated(ctx)
	assert.NoError(t, err)
	assert.True(t, updated)
	err = dep.UpdateCache(ctx)
	assert.NoError(t, err)

	dir := filepath.Join(d, "dir")
	err = os.Mkdir(dir, 0755)
	assert.NoError(t, err)

	// New Dir no update
	updated, err = dep.Updated(ctx)
	assert.NoError(t, err)
	assert.False(t, updated)
	err = dep.UpdateCache(ctx)
	assert.NoError(t, err)

	// Add a selected file
	f, err := os.Create(filepath.Join(dir, "tmp.md"))
	assert.NoError(t, err)
	_, err = f.Write([]byte("hello world"))
	assert.NoError(t, err)
	err = f.Close()
	assert.NoError(t, err)

	updated, err = dep.Updated(ctx)
	assert.NoError(t, err)
	assert.True(t, updated)
	err = dep.UpdateCache(ctx)
	assert.NoError(t, err)

	// Add a non-select file
	f, err = os.CreateTemp(dir, "tmp.txt")
	assert.NoError(t, err)
	_, err = f.Write([]byte("hello world"))
	assert.NoError(t, err)
	err = f.Close()
	assert.NoError(t, err)

	updated, err = dep.Updated(ctx)
	assert.NoError(t, err)
	assert.False(t, updated)
}

func TestHashFolderAndFilter(t *testing.T) {
	// create a temporary directory
	d, err := os.MkdirTemp("", "example")
	assert.NoError(t, err)
	ctx := context.Background()
	defer os.RemoveAll(d)

	dep := builders.NewDependencies("test", builders.NewDependency(d).WithPathSelect(shared.NewIgnore("*.md")))
	dep.Localize(d)

	updated, err := dep.Updated(ctx)
	assert.NoError(t, err)
	assert.True(t, updated)
	err = dep.UpdateCache(ctx)
	assert.NoError(t, err)

	// Adding only a directory shouldn't modify the hash
	dir := filepath.Join(d, "dir")
	err = os.Mkdir(dir, 0755)
	assert.NoError(t, err)

	updated, err = dep.Updated(ctx)
	assert.NoError(t, err)
	assert.False(t, updated)
	err = dep.UpdateCache(ctx)
	assert.NoError(t, err)

	// Add an ignored file shouldn't modify the hash
	f, err := os.Create(filepath.Join(dir, "tmp.md"))
	assert.NoError(t, err)
	_, err = f.Write([]byte("hello world"))
	assert.NoError(t, err)
	err = f.Close()
	assert.NoError(t, err)

	updated, err = dep.Updated(ctx)
	assert.NoError(t, err)
	assert.False(t, updated)
	err = dep.UpdateCache(ctx)
	assert.NoError(t, err)

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
