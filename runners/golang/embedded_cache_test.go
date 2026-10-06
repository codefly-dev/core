package golang_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/runners/golang"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

// Build and execute real binaries: a changed embedded configuration must be
// observable without changing a Go file or deleting the runner's cache.
func TestNativeBuildInvalidatesCacheOnEmbeddedYAMLChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	root := t.TempDir()
	write := func(name, body string) {
		t.Helper()
		require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0644))
	}
	write("go.mod", "module example.com/embedded-cache\n\ngo 1.21\n")
	write("cmd/server/main.go", `package main
import (
    "fmt"
    "example.com/embedded-cache/config"
)
func main() { fmt.Print(config.Text) }
`)
	write("config/config.go", `package config
import _ "embed"
//go:embed policy.yaml
var Text string
`)
	write("config/policy.yaml", "policy: first\n")
	env, err := golang.NewNativeGoRunner(ctx, root, "cmd/server")
	require.NoError(t, err)
	env.WithLocalCacheDir(t.TempDir())
	t.Cleanup(func() { require.NoError(t, env.Shutdown(context.Background())) })
	require.NoError(t, env.Init(ctx))
	run := func(want string, cached bool) {
		t.Helper()
		require.NoError(t, env.BuildBinary(ctx))
		require.Equal(t, cached, env.UsedCache())
		proc, err := env.Runner()
		require.NoError(t, err)
		output := shared.NewSliceWriter()
		proc.WithOutput(output)
		require.NoError(t, proc.Run(ctx))
		require.Equal(t, want, strings.TrimSpace(strings.Join(output.Snapshot(), "\n")))
	}
	run("policy: first", false)
	run("policy: first", true)
	write("config/policy.yaml", "policy: other\n") // same size; content, not mtime/size
	run("policy: other", false)
	run("policy: other", true)
	write("config/policy.yaml", "policy: first\n")
	run("policy: first", false)
	require.NoError(t, os.Remove(filepath.Join(root, "config/policy.yaml")))
	require.Error(t, env.BuildBinary(ctx), "missing embedded input must not reuse the previous binary")
}
