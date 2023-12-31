package configurations_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

type spec struct {
	ReadReplicas int `yaml:"readReplicas"`
}

func TestLoadSpec(t *testing.T) {
	ctx := context.Background()
	content := []byte(`awesome: hello
readReplicas: 1
`)
	var s spec
	err := configurations.LoadSpec(ctx, content, &s)
	assert.NoError(t, err)
	assert.Equal(t, 1, s.ReadReplicas)
}

func TestSerializeSpec(t *testing.T) {
	ctx := context.Background()
	s := spec{ReadReplicas: 1}
	content, err := configurations.SerializeSpec(ctx, s)
	assert.NoError(t, err)
	assert.Contains(t, string(content), "readReplicas")
	assert.Contains(t, string(content), "1")

	ts := testSpec{TestField: "testKind"}
	content, err = configurations.SerializeSpec(ctx, ts)
	assert.NoError(t, err)
	assert.Contains(t, string(content), "test-field")
	assert.Contains(t, string(content), "testKind")
}
