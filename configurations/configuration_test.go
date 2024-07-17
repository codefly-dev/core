package configurations_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

type testObject struct {
	Key   string
	Value string
}

type testUnmarshal struct {
	Top    string
	Nested struct {
		Value   string
		Values  []string
		Objects []testObject
	}
}

func TestYaml(t *testing.T) {
	ctx := context.Background()
	p := shared.MustSolvePath("testdata/configurations/config.yaml")
	info, err := configurations.ConfigurationInformationDataFromFile(ctx, "test", p, false)
	require.NoError(t, err)
	require.Equal(t, "test", info.Name)
	var config testUnmarshal
	err = configurations.InformationUnmarshal(info, &config)
	require.NoError(t, err)
}

func TestYamlArray(t *testing.T) {
	ctx := context.Background()
	p := shared.MustSolvePath("testdata/configurations/config_array.yaml")
	info, err := configurations.ConfigurationInformationDataFromFile(ctx, "test", p, false)
	require.NoError(t, err)
	var config testUnmarshal
	err = configurations.InformationUnmarshal(info, &config)
	require.NoError(t, err)
	require.Len(t, config.Nested.Values, 2)
}

func TestYamlArrayWithStruct(t *testing.T) {
	ctx := context.Background()
	p := shared.MustSolvePath("testdata/configurations/config_array_of_struct.yaml")
	info, err := configurations.ConfigurationInformationDataFromFile(ctx, "test", p, false)
	require.NoError(t, err)
	var config testUnmarshal
	err = configurations.InformationUnmarshal(info, &config)
	require.NoError(t, err)
	require.Len(t, config.Nested.Objects, 2)
}
