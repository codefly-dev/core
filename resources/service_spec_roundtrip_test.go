package resources_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

type CommonSpecSettings struct {
	Enabled bool `yaml:"enabled"`
}

type ExtendedSpecSettings struct {
	CommonSpecSettings `yaml:",inline"`
	Attempts           int `yaml:"attempts"`
}

type composedSpecSettings struct {
	ExtendedSpecSettings `yaml:",inline"`
	Optional             string `yaml:"optional,omitempty"`
	Nested               struct {
		Label string `yaml:"label"`
	} `yaml:"nested"`
}

func TestServiceSettingsPreserveInlineYAMLThroughUpdate(t *testing.T) {
	contents, err := os.ReadFile("testdata/service-spec-inline.yaml")
	require.NoError(t, err)
	var authored composedSpecSettings
	require.NoError(t, yaml.Unmarshal(contents, &authored))
	require.True(t, authored.Enabled)

	s := &resources.Service{Name: "settings-roundtrip", Spec: map[string]any{"extension": "preserved"}}
	require.NoError(t, s.UpdateSpecFromSettings(&authored))
	require.Equal(t, "preserved", s.Spec["extension"])
	require.Equal(t, true, s.Spec["enabled"])
	require.NotContains(t, s.Spec, "ExtendedSpecSettings")
	require.NotContains(t, s.Spec, "CommonSpecSettings")
	dir := t.TempDir()
	require.NoError(t, s.SaveAtDir(context.Background(), dir))
	persisted, err := os.ReadFile(filepath.Join(dir, resources.ServiceConfigurationName))
	require.NoError(t, err)
	require.NotContains(t, string(persisted), "ExtendedSpecSettings")
	require.NotContains(t, string(persisted), "CommonSpecSettings")
	s, err = resources.LoadFromDir[resources.Service](context.Background(), dir)
	require.NoError(t, err)
	var restored composedSpecSettings
	require.NoError(t, s.LoadSettingsFromSpec(&restored))
	require.Equal(t, authored, restored)
}

type invalidSpecSettings struct{}

func (invalidSpecSettings) MarshalYAML() (any, error) {
	return nil, errors.New("invalid settings")
}

func TestServiceSettingsUpdatePreservesUnspecifiedKeys(t *testing.T) {
	s := &resources.Service{Spec: map[string]any{"enabled": true, "optional": "retained", "extension": 3}}
	require.NoError(t, s.UpdateSpecFromSettings(&composedSpecSettings{}))
	require.Equal(t, false, s.Spec["enabled"], "explicit zero values must replace old settings")
	require.Equal(t, "retained", s.Spec["optional"], "omitted settings must retain the existing update semantics")
	require.Equal(t, 3, s.Spec["extension"])
	before, err := yaml.Marshal(s.Spec)
	require.NoError(t, err)
	for _, invalid := range []any{invalidSpecSettings{}, []string{"not a settings map"}} {
		require.Error(t, s.UpdateSpecFromSettings(invalid))
		after, err := yaml.Marshal(s.Spec)
		require.NoError(t, err)
		require.Equal(t, string(before), string(after), "a failed update must not mutate the existing spec")
	}
	require.NoError(t, s.UpdateSpecFromSettings(nil))
	after, err := yaml.Marshal(s.Spec)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after))
}
