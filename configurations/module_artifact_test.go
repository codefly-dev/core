package configurations

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func artifactFixture(t *testing.T) (string, *resources.Workspace) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "fixture")
	require.NoError(t, os.CopyFS(root, os.DirFS("testdata/module-artifact")))
	ws, err := resources.LoadWorkspaceFromDir(context.Background(), root)
	require.NoError(t, err)
	return root, ws
}

func TestModuleArtifactPerLoadAcquiresActualFilesOnce(t *testing.T) {
	ctx := context.Background()
	root, ws := artifactFixture(t)
	env := &resources.Environment{Name: "local"}
	reads := 0
	resolve := func(_ context.Context, ref *basev0.ConfigurationModuleArtifactReference) ([]byte, error) {
		reads++
		return os.ReadFile(filepath.Join(root, ref.Path))
	}
	reader, err := NewConfigurationLocalReader(ctx, ws)
	require.NoError(t, err)
	reader.WithModuleArtifactResolver(resolve)
	require.NoError(t, reader.Load(ctx, env))
	require.Equal(t, 1, reads)
	original := proto.Clone(reader.Configurations()[0]).(*basev0.Configuration)
	a := original.Infos[0].ConfigurationValues[0]
	require.NotNil(t, a.ResolvedModuleArtifact)
	require.NoError(t, os.WriteFile(filepath.Join(root, "public.json"), []byte("next\nexact\n"), 0600))
	// A generated credential appears after planning and is observed on normal load.
	require.NoError(t, os.WriteFile(filepath.Join(root, "configurations/local/generated.secret.env"), []byte("SIGNING_KEY=newly-generated\n"), 0600))
	require.NoError(t, reader.Load(ctx, env))
	require.Equal(t, 2, reads)
	var generated bool
	for _, c := range reader.Configurations() {
		for _, i := range c.Infos {
			for _, v := range i.ConfigurationValues {
				if v.ResolvedModuleArtifact != nil {
					require.Equal(t, "next\nexact\n", v.Value)
					require.NotEqual(t, a.ResolvedModuleArtifact.ArtifactIdentity.Digest, v.ResolvedModuleArtifact.ArtifactIdentity.Digest)
				}
				if i.Name == "generated" {
					generated = true
				}
			}
		}
	}
	require.True(t, generated)
	require.NotEqual(t, "next\nexact\n", a.Value)
	// Pure read has its own acquisition, not process overrides or old evidence.
	provided, err := ReadWorkspaceConfigurations(ctx, ws, env, resolve)
	require.NoError(t, err)
	require.Equal(t, 3, reads)
	require.NotEmpty(t, provided.Infos)
	require.NoError(t, os.Remove(filepath.Join(root, "public.json")))
	require.Error(t, reader.Load(ctx, env))
}

func TestModuleArtifactAddRemoveAndMissingResolver(t *testing.T) {
	ctx := context.Background()
	root, ws := artifactFixture(t)
	env := &resources.Environment{Name: "local"}
	_, err := ReadWorkspaceConfigurations(ctx, ws, env)
	require.ErrorContains(t, err, "resolver")
	declaration := filepath.Join(root, "configurations/local", moduleArtifactsFile)
	saved, err := os.ReadFile(declaration)
	require.NoError(t, err)
	require.NoError(t, os.Remove(declaration))
	require.NoError(t, os.WriteFile(filepath.Join(root, "configurations/shared/install.env"), []byte("UNCHANGED=ordinary\n"), 0600))
	reader, err := NewConfigurationLocalReader(ctx, ws)
	require.NoError(t, err)
	reader.WithModuleArtifactResolver(func(_ context.Context, r *basev0.ConfigurationModuleArtifactReference) ([]byte, error) {
		return os.ReadFile(filepath.Join(root, r.Path))
	})
	require.NoError(t, reader.Load(ctx, env)) // empty
	require.NoError(t, os.WriteFile(filepath.Join(root, "configurations/shared/install.env"), []byte("CONFIG=${profile}\nUNCHANGED=ordinary\n"), 0600))
	require.NoError(t, os.WriteFile(declaration, saved, 0600))
	require.NoError(t, reader.Load(ctx, env)) // one
	require.NoError(t, os.Remove(declaration))
	require.NoError(t, os.WriteFile(filepath.Join(root, "configurations/shared/install.env"), []byte("CONFIG=ordinary-again\nUNCHANGED=ordinary\n"), 0600))
	require.NoError(t, reader.Load(ctx, env)) // empty again
}

func TestModuleArtifactStrictDeclarationsAndLiteralTransition(t *testing.T) {
	for _, contents := range []string{
		`{"schemaVersion":1,"schema_version":1}`, `{"schemaVersion":1,"bindings":null}`, `{"schemaVersion":1,"bindings":[null]}`, `{"schemaVersion":2}`, `{"schemaVersion":1,"unknown":"PRIVATE-CANARY"}`, `{} {}`,
		`{"schemaVersion":1,"bindings":[{"configuration":"g","key":"K","source":{"module":"m","path":"../outside"}}]}`,
		`{"schemaVersion":1,"bindings":[{"configuration":"g","key":"K","source":{"module":"m","path":"x"}},{"configuration":"G","key":"k","source":{"module":"m","path":"x"}}]}`,
		`{"schemaVersion":1,"bindings":[{"configuration":"g","key":"K","resolvedModuleArtifact":{}}]}`,
		strings.Repeat(" ", maxModuleArtifactDeclarationBytes+1),
	} {
		t.Run(contents[:min(35, len(contents))], func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, moduleArtifactsFile), []byte(contents), 0600))
			_, err := loadModuleArtifactBindings(dir, nil)
			require.Error(t, err)
			require.NotContains(t, err.Error(), "PRIVATE-CANARY")
		})
	}
	for _, literal := range []*basev0.ConfigurationValue{{Key: "CONFIG", Value: "public"}, {Key: "CONFIG", Value: ProfileValueMarker, Secret: true}, {Key: "CONFIG", Value: "prefix" + ProfileValueMarker}} {
		root, _ := artifactFixture(t)
		_, err := loadModuleArtifactBindings(filepath.Join(root, "configurations/local"), []*basev0.ConfigurationInformation{{Name: "install", ConfigurationValues: []*basev0.ConfigurationValue{literal}}})
		require.Error(t, err)
	}
	root, _ := artifactFixture(t)
	layer, err := LoadProfileConfigurations(context.Background(), root, "configurations", []string{"local", "shared"})
	require.NoError(t, err)
	require.Empty(t, layer.Unsupplied)
	require.NoError(t, os.WriteFile(filepath.Join(root, "configurations/shared/install.env"), []byte("CONFIG=public\n"), 0600))
	_, err = LoadProfileConfigurations(context.Background(), root, "configurations", []string{"local"})
	require.Error(t, err)
}

func TestModuleArtifactInvocationAndEvidenceIngress(t *testing.T) {
	ctx := context.Background()
	root, ws := artifactFixture(t)
	reader, err := NewConfigurationLocalReader(ctx, ws)
	require.NoError(t, err)
	reader.WithModuleArtifactResolver(func(_ context.Context, r *basev0.ConfigurationModuleArtifactReference) ([]byte, error) {
		return os.ReadFile(filepath.Join(root, r.Path))
	})
	encoded, err := resources.EncodeWorkspaceConfigurationOverrides([]resources.WorkspaceConfigurationOverride{{Name: "INSTALL", Key: "config", Value: "same"}})
	require.NoError(t, err)
	t.Setenv(resources.WorkspaceConfigurationOverridesEnvironment, encoded)
	require.Error(t, reader.Load(ctx, &resources.Environment{Name: "local"}))
	for _, encoded := range []string{`[{"name":"g","key":"k","resolved_module_artifact":{}}]`, `[{"name":"g","key":"k","resolvedModuleArtifact":{}}]`} {
		_, err := resources.DecodeWorkspaceConfigurationOverrides(encoded)
		require.Error(t, err)
	}
	evidence := &basev0.Configuration{Infos: []*basev0.ConfigurationInformation{{ConfigurationValues: []*basev0.ConfigurationValue{{Key: "K", ResolvedModuleArtifact: &basev0.ResolvedConfigurationModuleArtifact{}}}}}}
	require.Error(t, resolveModuleArtifacts(ctx, []*basev0.Configuration{evidence}, nil))
}

func TestModuleArtifactReferenceReplacementAndOverrideChecks(t *testing.T) {
	ref := func(p string) *basev0.ConfigurationInformation {
		return &basev0.ConfigurationInformation{Name: "g", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "K", ModuleArtifact: &basev0.ConfigurationModuleArtifactReference{Module: "m", Path: p}}}}
	}
	overlay := newProfileOverlay()
	require.NoError(t, overlay.add("base", []*basev0.ConfigurationInformation{ref("a")}))
	require.NoError(t, overlay.add("derived", []*basev0.ConfigurationInformation{ref("b")}))
	require.Equal(t, "b", overlay.infos[0].ConfigurationValues[0].ModuleArtifact.Path)
	literal := &basev0.ConfigurationInformation{Name: "g", ConfigurationValues: []*basev0.ConfigurationValue{{Key: "k", Value: "a"}}}
	require.Error(t, overlay.add("later", []*basev0.ConfigurationInformation{literal}))
	_, err := overlayWorkspaceConfigurationOverride(ref("a"), literal, "m")
	require.Error(t, err)
	_, err = applyServiceConfigurationOverrides(map[string]*basev0.Configuration{"m/s": {Origin: "m/s", Infos: []*basev0.ConfigurationInformation{ref("a")}}}, []string{"m/s"}, `[{"service":"m/s","name":"g","key":"K","value":"a"}]`)
	require.Error(t, err)
	require.Error(t, checkArtifactOverlay(ref("a"), &basev0.ConfigurationInformation{Name: "g", Data: &basev0.ConfigurationData{}}, true))
}

func TestModuleArtifactComposedScopeAndDiscardedLiteral(t *testing.T) {
	root := t.TempDir()
	write := func(name, content string) {
		p := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0700))
		require.NoError(t, os.WriteFile(p, []byte(content), 0600))
	}
	write("solution/workspace.codefly.yaml", "name: solution\nlayout: modules\nmodules:\n  - name: host\n    path: ../host\n")
	write("host/module.codefly.yaml", "kind: module\nname: host\nservices: []\n")
	write("host/configurations/local/install.env", "CONFIG=${profile}\nOTHER=retained\n")
	write("solution/configurations/local/"+moduleArtifactsFile, `{"schemaVersion":1,"bindings":[{"configuration":"install","key":"CONFIG","source":{"module":"documents","path":"public.json"}}]}`)
	write("public.json", "public\nbytes\n")
	ws, err := resources.LoadWorkspaceFromDir(t.Context(), filepath.Join(root, "solution"))
	require.NoError(t, err)
	resolver := func(_ context.Context, r *basev0.ConfigurationModuleArtifactReference) ([]byte, error) {
		return os.ReadFile(filepath.Join(root, r.Path))
	}
	provided, err := ReadWorkspaceConfigurations(t.Context(), ws, resources.LocalEnvironment(), resolver)
	require.NoError(t, err)
	require.Equal(t, "host", provided.ComposedBy["install"])
	reader, err := NewConfigurationLocalReader(t.Context(), ws)
	require.NoError(t, err)
	reader.WithModuleArtifactResolver(resolver)
	require.NoError(t, reader.Load(t.Context(), resources.LocalEnvironment()))
	require.Empty(t, reader.CompositionRootWorkspaceConfigurationNames())
	write("host/configurations/local/install.env", "CONFIG=public\nOTHER=retained\n")
	_, err = ReadWorkspaceConfigurations(t.Context(), ws, resources.LocalEnvironment(), resolver)
	require.Error(t, err)
	// A whole imported group replacement cannot hide its bound destination.
	write("solution/workspace.codefly.yaml", "name: solution\nlayout: modules\nworkspaces:\n  - name: child\n    path: ../child\n")
	write("child/workspace.codefly.yaml", "name: child\nlayout: modules\n")
	write("child/configurations/local/install.env", "CONFIG=literal\n")
	ws, err = resources.LoadWorkspaceFromDir(t.Context(), filepath.Join(root, "solution"))
	require.NoError(t, err)
	_, err = ReadWorkspaceConfigurations(t.Context(), ws, resources.LocalEnvironment(), resolver)
	require.Error(t, err)
}

func TestModuleArtifactSelectedProfileDoesNotFallThrough(t *testing.T) {
	root, _ := artifactFixture(t)
	require.NoError(t, os.Remove(filepath.Join(root, "configurations/local/profile.codefly.yaml")))
	require.NoError(t, os.Remove(filepath.Join(root, "configurations/local", moduleArtifactsFile)))
	layer, err := LoadProfileConfigurations(t.Context(), root, "configurations", []string{"local", "shared"})
	require.NoError(t, err)
	require.Empty(t, layer.Infos)
	require.Len(t, layer.Layers, 1)
}

func TestModuleArtifactDeclarationRefusesFIFOWithoutOpeningPeer(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, syscall.Mkfifo(filepath.Join(dir, moduleArtifactsFile), 0600))
	_, err := loadModuleArtifactBindings(dir, nil)
	require.ErrorContains(t, err, "regular file")
}

func TestModuleArtifactCannotLaunderReferenceThroughProfileSlot(t *testing.T) {
	root, ws := artifactFixture(t)
	// Put the reference below the selected layer and the slot above it. An
	// invocation override must not turn that discarded binding into a literal.
	require.NoError(t, os.Remove(filepath.Join(root, "configurations/shared/install.env")))
	contents, err := os.ReadFile(filepath.Join(root, "configurations/local", moduleArtifactsFile))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(root, "configurations/shared", moduleArtifactsFile), contents, 0600))
	require.NoError(t, os.Remove(filepath.Join(root, "configurations/local", moduleArtifactsFile)))
	require.NoError(t, os.WriteFile(filepath.Join(root, "configurations/local/install.env"), []byte("CONFIG=${profile}\n"), 0600))
	encoded, err := resources.EncodeWorkspaceConfigurationOverrides([]resources.WorkspaceConfigurationOverride{{Name: "install", Key: "CONFIG", Value: "ordinary"}})
	require.NoError(t, err)
	t.Setenv(resources.WorkspaceConfigurationOverridesEnvironment, encoded)
	reader, err := NewConfigurationLocalReader(t.Context(), ws)
	require.NoError(t, err)
	require.ErrorIs(t, reader.Load(t.Context(), resources.LocalEnvironment()), ErrConfigurationConflict)
	_, err = ReadWorkspaceConfigurations(t.Context(), ws, resources.LocalEnvironment())
	require.ErrorIs(t, err, ErrConfigurationConflict)
}
