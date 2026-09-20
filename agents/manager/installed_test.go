package manager

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestInstalledUsesArtifactsNotAnAgentRoster(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	agents, err := Installed(t.Context(), resources.ServiceAgent)
	require.NoError(t, err)
	require.Empty(t, agents)
	for _, fixture := range []struct {
		publisher, name, version string
		mode                     os.FileMode
	}{
		{"example.test", "unlisted", "1.0.0", 0o755},
		{"example.test", "unlisted", "2.0.0", 0o755},
		{"example.test", "unlisted", "3.0.0", 0o644},
		{"example.test", "partial", "bad", 0o755},
		{"other.test", "unlisted", "0.1.0", 0o755},
	} {
		agent := &resources.Agent{Kind: resources.ServiceAgent, Publisher: fixture.publisher, Name: fixture.name, Version: fixture.version}
		path, err := agent.Path(t.Context())
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, nil, fixture.mode))
	}
	agents, err = Installed(t.Context(), resources.ServiceAgent)
	require.NoError(t, err)
	require.Len(t, agents, 2)
	require.Equal(t, "example.test/unlisted:2.0.0", agents[0].Identifier())
	require.Equal(t, "other.test/unlisted:0.1.0", agents[1].Identifier())
}
