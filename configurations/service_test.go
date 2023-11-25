package configurations_test

import (
	"os"
	"path"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

type testSpec struct {
	TestField string `yaml:"test-field"`
}

func TestSpecSave(t *testing.T) {
	s := &configurations.Service{Name: "testName"}
	out, err := yaml.Marshal(s)
	assert.NoError(t, err)
	assert.Contains(t, string(out), "testName")

	err = s.UpdateSpecFromSettings(&testSpec{TestField: "testKind"})
	assert.NoError(t, err)
	assert.NotNilf(t, s.Spec, "UpdateSpecFromSettings failed")
	_, ok := s.Spec["test-field"]
	assert.True(t, ok)

	// save to file
	tmp := t.TempDir()
	err = s.SaveAtDir(tmp)
	assert.NoError(t, err)
	// make sure it looks good
	content, err := os.ReadFile(path.Join(tmp, configurations.ServiceConfigurationName))
	assert.NoError(t, err)
	assert.Contains(t, string(content), "test-field")
	assert.Contains(t, string(content), "testKind")

	s, err = configurations.LoadFromDir[configurations.Service](tmp)
	assert.NoError(t, err)

	assert.NoError(t, err)
	var field testSpec
	err = s.LoadSettingsFromSpec(&field)
	assert.NoError(t, err)
	assert.Equal(t, "testKind", field.TestField)
}
