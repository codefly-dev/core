package configurations

import (
	"context"
	"path/filepath"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestWorkspaceOverlayDoesNotInventAnUnrecordedOrigin(t *testing.T) {
	trace, err := newConfigurationOrigins(t.TempDir())
	require.NoError(t, err)
	base := &basev0.ConfigurationInformation{Name: "app", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "RETAINED", Value: "private"}}}
	override := &basev0.ConfigurationInformation{Name: "app"}
	result, err := overlayWorkspaceConfigurationOverride(base, override, "host")
	require.NoError(t, err)
	trace.workspaceOverlay(base, override, result, "host")
	require.Empty(t, trace.project([]*basev0.ConfigurationInformation{result}), "an unrecorded parent must not become an empty-path origin")
}

func TestWorkspaceResolutionWithoutEvidenceMatchesDiagnosticRead(t *testing.T) {
	ctx := context.Background()
	dir, err := filepath.Abs("testdata/origins")
	require.NoError(t, err)
	workspace, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)
	env := &resources.Environment{Name: "staging", ConfigurationProfile: "staging"}
	plain, err := readWorkspaceConfigurations(ctx, workspace, env)
	require.NoError(t, err)
	evidence, err := ReadWorkspaceConfigurations(ctx, workspace, env)
	require.NoError(t, err)
	require.Empty(t, plain.Origins)
	require.Empty(t, plain.Decisions)
	require.Empty(t, plain.ProfileSelections)
	require.NotEmpty(t, evidence.Origins)
	require.NotEmpty(t, evidence.Decisions)
	require.Len(t, plain.Infos, len(evidence.Infos))
	for i := range plain.Infos {
		require.True(t, proto.Equal(plain.Infos[i], evidence.Infos[i]))
	}
	require.Equal(t, plain.ComposedBy, evidence.ComposedBy)
	require.Equal(t, plain.Ambiguous, evidence.Ambiguous)
	require.Equal(t, plain.Unsupplied, evidence.Unsupplied)
}
