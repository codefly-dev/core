package configurations_test

import (
	"gopkg.in/yaml.v3"
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestParseServiceEntry(t *testing.T) {
	tests := []struct {
		input       string
		service     string
		application string
	}{
		{"app/svc", "svc", "app"},
		{"svc", "svc", ""},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ref, err := configurations.ParseServiceInput(tt.input)
			assert.NoError(t, err)
			assert.Equalf(t, tt.service, ref.Name, "ParseServiceInput(%v) Unique failed", tt.input)
			assert.Equalf(t, tt.application, ref.Application, "ParseServiceInput(%v) Application failed", tt.input)
		})
	}
}

type testSpec struct {
	TestField string `yaml:"test-field"`
}

func TestSpecSave(t *testing.T) {
	s := &configurations.Service{Name: "testName"}
	out, err := yaml.Marshal(s)
	assert.NoError(t, err)
	assert.Contains(t, string(out), "testName")

	err = s.UpdateSpec(&testSpec{TestField: "testKind"})
	assert.NoError(t, err)
	assert.NotNilf(t, s.Spec, "UpdateSpec failed")

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
	err = s.LoadFromSpec(&field)
	assert.NoError(t, err)
	assert.Equal(t, "testKind", field.TestField)

}
