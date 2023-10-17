package configurations_test

import (
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
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

type globalConfigInput struct {
	createDefaultProject bool
}

func (g *globalConfigInput) Fetch() error {
	return nil
}

func (g *globalConfigInput) Organization() string {
	return "org:test"
}

func (g *globalConfigInput) Domain() string {
	return "github.com/org/test"
}

func (g *globalConfigInput) CreateDefaultProject() bool {
	return g.createDefaultProject
}

func (g *globalConfigInput) ProjectBuilder() configurations.ProjectBuilder {
	return &projectBuilder{defaultProjectName: "test_project_name"}
}

var g globalConfigInput

type projectBuilder struct {
	defaultProjectName string
}

func (b *projectBuilder) DefaultProjectName() string {
	return b.defaultProjectName
}

func (b *projectBuilder) ProjectName() string {
	return b.defaultProjectName
}

func (b *projectBuilder) Fetch() error {
	return nil
}

func (b *projectBuilder) RelativePath() string {
	return b.DefaultProjectName()
}

func (b *projectBuilder) Style() configurations.ProjectStyle {
	return configurations.ProjectStyleMonorepo
}

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
	shared.SetTrace(true)

	g = globalConfigInput{createDefaultProject: true}

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

	// We should also have a project from the option
	assert.Equal(t, 1, len(configurations.MustCurrent().Projects))
	assert.Equal(t, g.ProjectBuilder().ProjectName(), configurations.MustCurrent().Projects[0].Name)
	assert.Equal(t, g.ProjectBuilder().ProjectName(), configurations.MustCurrent().CurrentProject)
	assert.Equal(t, g.ProjectBuilder().RelativePath(), configurations.MustCurrent().Projects[0].RelativePath)

	assert.True(t, fromTmp.Exists("projects/test_project_name/project.codefly.yaml"))

	application := "test_application"
	app, err := configurations.NewApplication(application)
	assert.NoError(t, err)
	assert.Equal(t, application, app.Name)
	assert.Equal(t, application, app.RelativePath)
	assert.Equal(t, application, configurations.MustCurrentApplication().Name)
	assert.Equal(t, application, configurations.MustCurrentProject().CurrentApplication)

	assert.True(t, fromTmp.Exists("projects/test_project_name/test_application"))
	assert.True(t, fromTmp.Exists("projects/test_project_name/test_application/application.codefly.yaml"))
}
