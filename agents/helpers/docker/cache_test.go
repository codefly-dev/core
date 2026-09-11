package docker

import (
	"archive/tar"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestCacheArguments(t *testing.T) {
	policy := &builderv0.BuildCacheOptions{Backend: "registry", Scope: "workspace/service/app/trusted", Imports: []string{"ghcr.io/org/cache"}, Exports: []string{"ghcr.io/org/cache"}, Mode: "max"}
	encoded, err := proto.Marshal(policy)
	require.NoError(t, err)
	decoded := &builderv0.BuildCacheOptions{}
	require.NoError(t, proto.Unmarshal(encoded, decoded))
	args, err := CacheArguments(decoded, []string{"linux/amd64"})
	require.NoError(t, err)
	require.Equal(t, "--cache-from", args[0])
	require.Equal(t, "--cache-to", args[2])
	require.Equal(t, args[1]+",mode=max", args[3])
	require.Equal(t, "--pull", args[4])
	repeat, err := CacheArguments(policy, []string{"linux/amd64"})
	require.NoError(t, err)
	require.Equal(t, args, repeat)
	arm, err := CacheArguments(policy, []string{"linux/arm64"})
	require.NoError(t, err)
	require.NotEqual(t, args[1], arm[1])
	policy.Scope = "workspace/other/app/trusted"
	other, err := CacheArguments(policy, []string{"linux/amd64"})
	require.NoError(t, err)
	require.NotEqual(t, args[1], other[1])
	multi, err := CacheArguments(policy, []string{"linux/amd64", "linux/arm64"})
	require.NoError(t, err)
	require.Len(t, multi, 9)
	require.NotEqual(t, multi[1], multi[3])
	require.Equal(t, multi[1]+",mode=max", multi[5])
	require.Equal(t, multi[3]+",mode=max", multi[7])
	policy.Mode = ""
	defaults, err := CacheArguments(policy, []string{"linux/amd64"})
	require.NoError(t, err)
	require.Contains(t, defaults[3], ",mode=min")
	policy.Exports = nil
	readOnly, err := CacheArguments(policy, []string{"linux/amd64"})
	require.NoError(t, err)
	require.NotContains(t, readOnly, "--cache-to")
}

func TestCacheRejectsUnsupportedAndInjectedOptions(t *testing.T) {
	for _, mutate := range []func(*builderv0.BuildCacheOptions){
		func(c *builderv0.BuildCacheOptions) { c.Backend = "gha" },
		func(c *builderv0.BuildCacheOptions) { c.Scope = "" },
		func(c *builderv0.BuildCacheOptions) { c.Mode = "other" },
		func(c *builderv0.BuildCacheOptions) { c.Imports = []string{"ghcr.io/org/cache,mode=max"} },
		func(c *builderv0.BuildCacheOptions) { c.Exports = []string{"https://user:secret@ghcr.io/org/cache"} },
		func(c *builderv0.BuildCacheOptions) { c.Exports = []string{"ghcr.io/org/cache:latest"} },
	} {
		policy := &builderv0.BuildCacheOptions{Backend: "registry", Scope: "service"}
		mutate(policy)
		_, err := CacheArguments(policy, []string{"linux/amd64"})
		require.Error(t, err)
	}
	args, err := CacheArguments(nil, nil)
	require.NoError(t, err)
	require.Empty(t, args)
}

func TestBuildContextExcludesIgnoredDirectoryContents(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, ".secrets"), 0755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "nested"), 0755))
	for name, data := range map[string]string{".secrets/token": "hidden", "nested/private.key": "hidden", "nested/public.key": "public", "Dockerfile": "FROM scratch", ".dockerignore": ".secrets\n**/*.key\n!nested/public.key\n"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(data), 0600))
	}
	builder := &Builder{BuilderConfiguration: BuilderConfiguration{Root: root, Ignorefile: ".dockerignore"}}
	archive, err := builder.createTarArchive(context.Background())
	require.NoError(t, err)
	reader := tar.NewReader(archive)
	names := []string{}
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		require.NoError(t, err)
		names = append(names, header.Name)
		data, err := io.ReadAll(reader)
		require.NoError(t, err)
		require.False(t, strings.Contains(string(data), "hidden"))
	}
	require.Contains(t, names, "nested/public.key")
	require.NotContains(t, names, ".secrets/token")
	require.NotContains(t, names, "nested/private.key")
}

func TestCacheBuildStillVerifiesArchitecture(t *testing.T) {
	policy := &builderv0.BuildCacheOptions{Backend: "registry", Scope: "service", Imports: []string{"ghcr.io/org/cache"}}
	backend := &scriptedBackend{architecture: "amd64"}
	builder := newTestBuilder(t, BuilderConfiguration{Root: t.TempDir(), Dockerfile: "Dockerfile", Destination: resources.NewDockerImage("test/cache:v1"), Output: io.Discard, Platform: "linux/arm64", Cache: policy}, backend)
	_, err := builder.Build(context.Background())
	require.ErrorContains(t, err, "exec format error")
	require.Same(t, policy, backend.cache)
	policy.Backend = "unsupported"
	_, err = builder.Build(context.Background())
	require.ErrorContains(t, err, "unsupported")
	require.Equal(t, 1, backend.calls)
}
