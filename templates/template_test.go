package templates_test

import (
	"context"
	"embed"
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"github.com/stretchr/testify/assert"
)

type testData struct {
	Test string
}

func testCopyAndApplyTemplateToDir(t *testing.T, fs shared.FileSystem, dir shared.Dir) {
	ctx := context.Background()
	dest := t.TempDir()
	destination := shared.NewDir(dest)
	err := templates.CopyAndApply(ctx, fs, dir, destination, testData{Test: "test"})
	assert.NoError(t, err)

	p := path.Join(dest, "template.txt")
	_, err = os.Stat(p)
	assert.NoError(t, err)

	content, err := os.ReadFile(p)
	assert.NoError(t, err)
	assert.Equal(t, "test", string(content))

	p = path.Join(dest, "test")
	_, err = os.Stat(p)
	assert.NoError(t, err)

	p = path.Join(dest, "test/other_template.txt")
	_, err = os.Stat(p)
	assert.NoError(t, err)

	content, err = os.ReadFile(p)
	assert.NoError(t, err)
	assert.Equal(t, "other test", string(content))
}

func TestCopyAndApplyTemplateToDirEmbed(t *testing.T) {
	fs := shared.Embed(test)
	dir := shared.NewDir("testdata")
	testCopyAndApplyTemplateToDir(t, fs, dir)
}

//go:embed testdata/*
var test embed.FS

func TestCopyAndApplyTemplateToDirLocal(t *testing.T) {
	dir := shared.MustLocal("testdata")
	fs := shared.NewDirReader()
	testCopyAndApplyTemplateToDir(t, fs, dir)
}
