package resources_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"

	"github.com/stretchr/testify/require"
)

type testSpec struct {
	ReadReplicas int `yaml:"readReplicas"`
}

func TestLoadSpec(t *testing.T) {
	ctx := context.Background()
	content := []byte(`awesome: hello
readReplicas: 1
`)
	var s testSpec
	err := resources.LoadSpec(ctx, content, &s)
	require.NoError(t, err)
	require.Equal(t, 1, s.ReadReplicas)
}

func TestSerializeSpec(t *testing.T) {
	ctx := context.Background()
	s := testSpec{ReadReplicas: 1}
	content, err := resources.SerializeSpec(ctx, s)
	require.NoError(t, err)
	require.Contains(t, string(content), "readReplicas")
	require.Contains(t, string(content), "1")
}

func TestAny(t *testing.T) {
	{
		value := 2
		p, err := resources.ConvertToAnyPb(value)
		require.NoError(t, err)
		back, err := resources.FromAnyPb[int](p)
		require.NoError(t, err)
		require.Equal(t, value, *back)
	}
	{
		value := []string{"a", "b"}
		p, err := resources.ConvertToAnyPb(value)
		require.NoError(t, err)
		back, err := resources.FromAnyPb[[]string](p)
		require.NoError(t, err)
		require.Equal(t, value, *back)
	}
}

func TestConvertSpec(t *testing.T) {

	sp := map[string]interface{}{}
	s, err := resources.ConvertSpec(sp)
	require.NoError(t, err)

	sp = map[string]interface{}{
		"string": "test",
		"array":  []string{"a", "b"},
	}
	s, err = resources.ConvertSpec(sp)
	require.NoError(t, err)

	require.Equal(t, "test", *shared.Must(resources.FromAnyPb[string](s.Fields["string"].Value)))
	require.Equal(t, []string{"a", "b"}, *shared.Must(resources.FromAnyPb[[]string](s.Fields["array"].Value)))
}
