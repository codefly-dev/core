package docker

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestRegistryCacheAcrossCleanBuilders(t *testing.T) {
	if os.Getenv("CODEFLY_TEST_REGISTRY_CACHE") != "1" {
		t.Skip("set CODEFLY_TEST_REGISTRY_CACHE=1 to run isolated Docker registry integration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	run := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "docker", args...)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		require.NoError(t, err, "%s\n%s", out, stderr.String())
		return strings.TrimSpace(string(out))
	}
	name := fmt.Sprintf("codefly-cache-%d", time.Now().UnixNano())
	run("network", "create", name)
	t.Cleanup(func() { exec.Command("docker", "network", "rm", name).Run() })
	registry := name + ".local"
	run("run", "-d", "--name", registry, "--network", name, "registry:2")
	t.Cleanup(func() { exec.Command("docker", "rm", "-f", registry).Run() })
	root := t.TempDir()
	config := filepath.Join(t.TempDir(), "buildkitd.toml")
	require.NoError(t, os.WriteFile(config, []byte(fmt.Sprintf("[registry.\"%s:5000\"]\n  http = true\n", registry)), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM scratch\nCOPY dependency /dependency\nCOPY source /source\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "dependency"), []byte("lock-v1"), 0600))
	policy := &builderv0.BuildCacheOptions{Backend: "registry", Scope: "service/app/trusted", Mode: "max", Imports: []string{registry + ":5000/cache"}, Exports: []string{registry + ":5000/cache"}}
	for i := 0; i < 3; i++ {
		builderName := fmt.Sprintf("%s-%d", name, i)
		run("buildx", "create", "--name", builderName, "--driver", "docker-container", "--driver-opt", "network="+name, "--buildkitd-config", config)
		t.Cleanup(func() { exec.Command("docker", "buildx", "rm", builderName).Run() })
		t.Setenv("BUILDX_BUILDER", builderName)
		source := "source-v1"
		if i > 0 {
			source = "source-v2"
		}
		if i == 2 {
			require.NoError(t, os.WriteFile(filepath.Join(root, "dependency"), []byte("lock-v2"), 0600))
		}
		require.NoError(t, os.WriteFile(filepath.Join(root, "source"), []byte(source), 0600))
		var output bytes.Buffer
		tag := fmt.Sprintf("%s:%d", name, i)
		t.Cleanup(func() { exec.Command("docker", "image", "rm", tag).Run() })
		builder, err := NewBuilder(BuilderConfiguration{Root: root, Dockerfile: "Dockerfile", Destination: resources.NewDockerImage(tag), Output: &output, Platform: "linux/amd64", Cache: policy})
		require.NoError(t, err)
		result, err := builder.Build(ctx)
		require.NoError(t, err, "%s", output.String())
		t.Logf("run %d wall=%s\n%s", i, result.Duration, output.String())
		if i == 1 {
			require.Contains(t, output.String(), "CACHED")
		}
		if i == 2 {
			require.NotContains(t, output.String(), "CACHED")
		}
		id := run("create", "--platform", "linux/amd64", tag, "unused")
		t.Cleanup(func() { exec.Command("docker", "rm", "-f", id).Run() })
		dest := t.TempDir()
		run("cp", id+":/source", filepath.Join(dest, "source"))
		run("rm", id)
		data, err := os.ReadFile(filepath.Join(dest, "source"))
		require.NoError(t, err)
		require.Equal(t, source, string(data))
	}
}
