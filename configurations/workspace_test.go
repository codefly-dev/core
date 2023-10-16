package configurations_test

import (
	"github.com/hygge-io/hygge/pkg/configurations"
	"github.com/hygge-io/hygge/pkg/core"
	"github.com/stretchr/testify/assert"
	"os"
	"path"
	"testing"
)

type override struct {
}

func (o override) Override(p string) bool {
	return false
}

type globalGetterTest struct {
}

func (g *globalGetterTest) RelativePath() string {
	//TODO implement me
	panic("implement me")
}

func (g *globalGetterTest) Style() configurations.ProjectStyle {
	//TODO implement me
	panic("implement me")
}

func (g *globalGetterTest) Organization() string {
	return "org:test"
}

func (g *globalGetterTest) Domain() string {
	return "github.com/org/test"
}

func (g *globalGetterTest) CreateDefaultProject() bool {
	return true
}

func (g *globalGetterTest) ProjectGetter() configurations.ProjectBuilder {
	// convenient
	return g
}

func (g *globalGetterTest) DefaultProjectName() string {
	return "test:project_name"
}

func (g *globalGetterTest) ProjectName() string {
	return g.DefaultProjectName()
}

func (g *globalGetterTest) Fetch() error {
	return nil
}

var g globalGetterTest
var over override

type tmpPath struct {
	root string
}

func (tp tmpPath) Exists(p string) bool {
	if _, err := os.Stat(path.Join(tp.root, p)); err == nil {
		return true
	}
	return false
}

func TestGlobal(t *testing.T) {
	core.SetDebug(true)

	// Temporary directory
	tmp := t.TempDir()
	fromTmp := tmpPath{root: tmp}
	configurations.OverrideWorkspaceConfigDir(path.Join(tmp, "global"))
	configurations.OverrideWorkspaceProjectRoot(path.Join(tmp, "projects"))
	configurations.InitGlobal(&g, &over)
	config, err := configurations.Current()
	assert.NoError(t, err)
	assert.Equal(t, path.Join(tmp, "global"), config.Dir())

	assert.True(t, fromTmp.Exists("global/codefly.yaml"))

	// Check that we have a default project
	configurations.Reset()
	assert.Equal(t, path.Join(tmp, "global"), configurations.MustCurrent().Dir())
	assert.Equal(t, g.Organization(), configurations.MustCurrent().Organization)
	assert.Equal(t, g.Domain(), configurations.MustCurrent().Domain)

	assert.True(t, fromTmp.Exists("projects/test:project_name/project.codefly.yaml"))

	assert.Equal(t, 1, len(configurations.MustCurrent().Projects))
	assert.Equal(t, g.ProjectName(), configurations.MustCurrent().Projects[0].Name)
	assert.Equal(t, g.DefaultProjectName(), configurations.MustCurrent().Projects[0].RelativePath)
	assert.Equal(t, g.DefaultProjectName(), configurations.MustCurrent().CurrentProject)

	application := "test:applications"
	app, err := configurations.NewApplication(application)
	assert.NoError(t, err)
	assert.Equal(t, application, app.Name)
	assert.Equal(t, application, app.RelativePath)
	assert.Equal(t, application, configurations.MustCurrentApplication().Name)
	assert.Equal(t, application, configurations.MustCurrentProject().CurrentApplication)

	assert.True(t, fromTmp.Exists("projects/test:project_name/test:applications"))
	assert.True(t, fromTmp.Exists("projects/test:project_name/test:applications/applications.codefly.yaml"))
}
