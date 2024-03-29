package templates_test

import (
	"context"
	"embed"
	"os"
	"path"
	"strings"
	"testing"

	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/templates"
	"github.com/stretchr/testify/assert"
)

type testData struct {
	Test string
}

type replacer struct{}

func (t replacer) Apply(_ context.Context, from string, to string) error {
	// replace est by oast
	content, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	content = []byte(strings.ReplaceAll(string(content), "est", "oast"))
	return os.WriteFile(to, content, 0600)
}

func testCopyAndApplyTemplateToDir(t *testing.T, fs shared.FileSystem, dir string) {
	ctx := context.Background()
	dest := t.TempDir()

	defer os.RemoveAll(dest)

	err := templates.CopyAndApply(ctx, fs, dir, dest, testData{Test: "test"})
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

const testdata = "testdata"

func TestCopyAndApplyTemplateToDirEmbed(t *testing.T) {
	fs := shared.Embed(test)
	dir := testdata
	testCopyAndApplyTemplateToDir(t, fs, dir)
}

//go:embed testdata/*
var test embed.FS

func TestCopyAndApplyTemplateToDirLocal(t *testing.T) {
	dir := testdata
	fs := shared.NewDirReader()
	testCopyAndApplyTemplateToDir(t, fs, dir)
}

func testCopyAndReplaceToDir(t *testing.T, fs shared.FileSystem, dir string) {
	ctx := context.Background()
	dest := t.TempDir()

	defer os.RemoveAll(dest)

	err := templates.CopyAndVisit(ctx, fs, dir, dest, nil, replacer{})
	assert.NoError(t, err)

	p := path.Join(dest, "template.txt.tmpl")
	_, err = os.Stat(p)
	assert.NoError(t, err)

	content, err := os.ReadFile(p)
	assert.NoError(t, err)
	assert.Equal(t, "{{.Toast}}", string(content))

	p = path.Join(dest, "test")
	_, err = os.Stat(p)
	assert.NoError(t, err)

	p = path.Join(dest, "test/other_template.txt.tmpl")
	_, err = os.Stat(p)
	assert.NoError(t, err)

	content, err = os.ReadFile(p)
	assert.NoError(t, err)
	assert.Equal(t, "other {{.Toast}}", string(content))
}

func TestCopyAndVisitEmbed(t *testing.T) {
	fs := shared.Embed(test)
	dir := testdata
	testCopyAndReplaceToDir(t, fs, dir)
}

func TestCopyAndVisitLocal(t *testing.T) {
	dir := testdata
	fs := shared.NewDirReader()
	testCopyAndReplaceToDir(t, fs, dir)
}
