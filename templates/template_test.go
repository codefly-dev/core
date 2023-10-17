package templates_test

import (
	"embed"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"github.com/stretchr/testify/assert"
	"os"
	"path"
	"testing"
)

type testData struct {
	Test string
}

func testCopyAndApplyTemplateToDir(t *testing.T, fs templates.FileSystem, dir shared.Dir) {
	logger := shared.NewLogger("templates.TestCopyAndApplyTemplateToDir")
	dest := t.TempDir()
	destination := shared.NewDir(dest)
	err := templates.CopyAndApply(logger, fs, dir, destination, testData{Test: "test"})
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
	fs := templates.NewEmbeddedFileSystem(test)
	dir := shared.NewDir("testdata")
	testCopyAndApplyTemplateToDir(t, fs, dir)
}

//go:embed testdata/*
var test embed.FS

func TestCopyAndApplyTemplateToDirLocal(t *testing.T) {
	dir := shared.MustLocal("testdata")
	fs := templates.NewDirReader()
	testCopyAndApplyTemplateToDir(t, fs, dir)
}
