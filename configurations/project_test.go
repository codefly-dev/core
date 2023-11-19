package configurations_test

import (
	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
	"os"
	"path"
	"testing"
)

func TestCreation(t *testing.T) {
	p, err := configurations.LoadProjectFromDir("testdata/project")
	assert.NoError(t, err)
	assert.Equal(t, "codefly-platform", p.Name)
	assert.Equal(t, 2, len(p.Applications))
	assert.Equal(t, "web", p.Applications[0].Name)
	assert.Equal(t, "management", p.Applications[1].Name)
	assert.Equal(t, "web", p.Current())

	// Save and make sure we preserve the "current application" convention
	tmpDir := t.TempDir()
	err = p.SaveToDir(tmpDir)
	assert.NoError(t, err)
	content, err := os.ReadFile(path.Join(tmpDir, configurations.ProjectConfigurationName))
	assert.NoError(t, err)
	assert.Contains(t, string(content), "web*")
	p, err = configurations.LoadProjectFromDir(tmpDir)
	assert.NoError(t, err)
	assert.Equal(t, "web", p.Current())
}
