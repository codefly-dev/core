package configurations_test

import (
	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
	"testing"
)

type spec struct {
	ReadReplicas int `yaml:"readReplicas"`
}

func TestLoadSpec(t *testing.T) {
	content := []byte(`awesome: hello
readReplicas: 1
`)
	var s spec
	err := configurations.LoadSpec(content, &s, nil)
	assert.NoError(t, err)
	assert.Equal(t, 1, s.ReadReplicas)

}
