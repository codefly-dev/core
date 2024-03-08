package configurations_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/shared"

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

func TestAny(t *testing.T) {
	{
		value := 2
		p, err := configurations.ConvertToAnyPb(value)
		assert.NoError(t, err)
		back, err := configurations.FromAnyPb[int](p)
		assert.NoError(t, err)
		assert.Equal(t, value, *back)
	}
	{
		value := []string{"a", "b"}
		p, err := configurations.ConvertToAnyPb(value)
		assert.NoError(t, err)
		back, err := configurations.FromAnyPb[[]string](p)
		assert.NoError(t, err)
		assert.Equal(t, value, *back)
	}
}

func TestConvertSpec(t *testing.T) {

	sp := map[string]interface{}{}
	s, err := configurations.ConvertSpec(sp)
	assert.NoError(t, err)

	sp = map[string]interface{}{
		"string": "test",
		"array":  []string{"a", "b"},
	}
	s, err = configurations.ConvertSpec(sp)
	assert.NoError(t, err)

	assert.Equal(t, "test", *shared.Must(configurations.FromAnyPb[string](s.Fields["string"].Value)))
	assert.Equal(t, []string{"a", "b"}, *shared.Must(configurations.FromAnyPb[[]string](s.Fields["array"].Value)))
}
