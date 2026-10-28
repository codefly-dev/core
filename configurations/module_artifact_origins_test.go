package configurations

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func artifactOrigins(t *testing.T, provided *WorkspaceConfigurations) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, origin := range provided.Origins {
		out[origin.Group+"/"+origin.Key] = origin.File
	}
	evidence, err := json.Marshal(struct {
		Origins   []*ConfigurationOrigin
		Decisions []*ConfigurationDecision
	}{provided.Origins, provided.Decisions})
	require.NoError(t, err)
	for _, info := range provided.Infos {
		for _, value := range info.ConfigurationValues {
			if value.ResolvedModuleArtifact != nil {
				require.NotContains(t, string(evidence), value.ResolvedModuleArtifact.ArtifactIdentity.Digest)
			}
		}
	}
	require.NotContains(t, string(evidence), "PUBLIC-CONTENT-CANARY")
	require.NotContains(t, string(evidence), "op://PRIVATE-CANARY")
	return out
}

func TestModuleArtifactOriginsIncludeNewGroupSlotAndDerivedReplacement(t *testing.T) {
	root, ws := artifactFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "public.json"), []byte("PUBLIC-CONTENT-CANARY"), 0600))
	resolver := func(_ context.Context, r *basev0.ConfigurationModuleArtifactReference) ([]byte, error) {
		return os.ReadFile(filepath.Join(root, r.Path))
	}
	provided, err := ReadWorkspaceConfigurations(t.Context(), ws, resources.LocalEnvironment(), resolver)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"install/CONFIG": "configurations/local/" + moduleArtifactsFile, "second/CONFIG": "configurations/local/" + moduleArtifactsFile, "install/UNCHANGED": "configurations/shared/install.env"}, artifactOrigins(t, provided))
	require.Len(t, provided.Decisions, 1)
	require.True(t, provided.Decisions[0].Final)
	require.Equal(t, []*ConfigurationOrigin{{Group: "install", Key: "CONFIG", File: "configurations/local/" + moduleArtifactsFile}}, provided.Decisions[0].Selected)
	// Replace a reference in a further derived profile; the newest declaration
	// supplies the value while the other bound group retains its original file.
	review := filepath.Join(root, "configurations/review")
	require.NoError(t, os.MkdirAll(review, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(review, ProfileDerivationFile), []byte("derives-from: local\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(review, moduleArtifactsFile), []byte(`{"schemaVersion":1,"bindings":[{"configuration":"install","key":"CONFIG","source":{"module":"documents","path":"public.json"}}]}`), 0600))
	provided, err = ReadWorkspaceConfigurations(t.Context(), ws, &resources.Environment{Name: "review", ConfigurationProfile: "review"}, resolver)
	require.NoError(t, err)
	require.Equal(t, "configurations/review/"+moduleArtifactsFile, artifactOrigins(t, provided)["install/CONFIG"])
	require.Equal(t, "configurations/local/"+moduleArtifactsFile, artifactOrigins(t, provided)["second/CONFIG"])
	require.Len(t, provided.Decisions, 2)
	require.False(t, provided.Decisions[0].Final)
	require.True(t, provided.Decisions[1].Final)
	require.Equal(t, "configurations/review/"+moduleArtifactsFile, provided.Decisions[1].Selected[0].File)
	require.Equal(t, "configurations/local/"+moduleArtifactsFile, provided.Decisions[1].Shadowed[0].File)
}

func TestModuleArtifactOriginsSurviveRetainedComposedClone(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0700))
		require.NoError(t, os.WriteFile(p, []byte(content), 0600))
	}
	write("solution/workspace.codefly.yaml", "name: solution\nlayout: modules\nmodules:\n  - name: host\n    path: ../host\n")
	write("host/module.codefly.yaml", "kind: module\nname: host\nservices: []\n")
	write("host/configurations/local/install.env", "OTHER=ordinary\n")
	write("host/configurations/local/"+moduleArtifactsFile, `{"schemaVersion":1,"bindings":[{"configuration":"install","key":"CONFIG","source":{"module":"documents","path":"public.json"}}]}`)
	write("solution/configurations/local/install.env", "OTHER=overridden\n")
	write("public.json", "PUBLIC-CONTENT-CANARY")
	ws, err := resources.LoadWorkspaceFromDir(t.Context(), filepath.Join(root, "solution"))
	require.NoError(t, err)
	provided, err := ReadWorkspaceConfigurations(t.Context(), ws, resources.LocalEnvironment(), func(_ context.Context, r *basev0.ConfigurationModuleArtifactReference) ([]byte, error) {
		return os.ReadFile(filepath.Join(root, r.Path))
	})
	require.NoError(t, err)
	require.Equal(t, map[string]string{"install/CONFIG": "../host/configurations/local/" + moduleArtifactsFile, "install/OTHER": "configurations/local/install.env"}, artifactOrigins(t, provided))
	require.Equal(t, "host", provided.ComposedBy["install"])
	require.Len(t, provided.Decisions, 1)
	require.True(t, provided.Decisions[0].Final)
	require.Equal(t, "configurations/local/install.env", provided.Decisions[0].Selected[0].File)
}

func TestModuleArtifactAcquisitionRefusalNamesFailedOfferWithoutResolverText(t *testing.T) {
	root, ws := artifactFixture(t)
	declaration := `{"schemaVersion":1,"bindings":[{"configuration":"install","key":"CONFIG","source":{"module":"documents","path":"public.json"}},{"configuration":"second","key":"CONFIG","source":{"module":"documents","path":"missing.json"}}]}`
	require.NoError(t, os.WriteFile(filepath.Join(root, "configurations/local", moduleArtifactsFile), []byte(declaration), 0600))
	_, err := ReadWorkspaceConfigurations(t.Context(), ws, resources.LocalEnvironment(), func(_ context.Context, r *basev0.ConfigurationModuleArtifactReference) ([]byte, error) {
		data, readErr := os.ReadFile(filepath.Join(root, r.Path))
		if readErr != nil {
			return nil, fmt.Errorf("op://PRIVATE-CANARY: %w", readErr)
		}
		return data, nil
	})
	require.Error(t, err)
	for _, context := range []string{`"documents"`, `"missing.json"`, `"second"/"CONFIG"`, `configurations/local/` + moduleArtifactsFile} {
		require.Contains(t, err.Error(), context)
	}
	require.NotContains(t, err.Error(), "op://PRIVATE-CANARY")
	require.NotContains(t, err.Error(), root)
}

func TestModuleArtifactOriginDoesNotReattributeSameLayerOrdinaryKeys(t *testing.T) {
	root, ws := artifactFixture(t)
	require.NoError(t, os.WriteFile(filepath.Join(root, "configurations/local/install.env"), []byte("CONFIG=${profile}\nUNCHANGED=current-layer\n"), 0600))
	provided, err := ReadWorkspaceConfigurations(t.Context(), ws, resources.LocalEnvironment(), func(_ context.Context, r *basev0.ConfigurationModuleArtifactReference) ([]byte, error) {
		return os.ReadFile(filepath.Join(root, r.Path))
	})
	require.NoError(t, err)
	origins := artifactOrigins(t, provided)
	require.Equal(t, "configurations/local/"+moduleArtifactsFile, origins["install/CONFIG"])
	require.Equal(t, "configurations/local/install.env", origins["install/UNCHANGED"])
	for _, decision := range provided.Decisions {
		require.True(t, decision.Final)
		require.NotEmpty(t, decision.Selected)
	}
}
