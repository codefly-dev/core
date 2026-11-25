package golang_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/golang"
)

// A playground composes its services from outside its own directory. The
// container backend mounted the workspace only and then worked in the
// service's source directory, which did not exist inside the container:
// `go mod download` exited 127 on "chdir to cwd ... no such file or directory".
//
// The service here lives beside the workspace and replaces a module with a
// directory beside itself, so the build needs both inside the container. It
// also proves the mounts are read-only: the source belongs to its checkout.
func TestDockerRunBuildsSourceComposedFromOutsideTheWorkspace(t *testing.T) {
	ctx := context.Background()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	workspace := filepath.Join(root, "playground")
	require.NoError(t, os.MkdirAll(workspace, 0o755))
	service := filepath.Join(root, "checkout", "services", "local", "code")
	writeFile(t, root, "checkout/libraries/answer/go.mod", "module example.com/answer\n\ngo 1.21\n")
	writeFile(t, root, "checkout/libraries/answer/answer.go", "package answer\n\nfunc Value() string { return \"composed\" }\n")
	writeFile(t, root, "checkout/services/local/code/go.mod",
		"module example.com/local\n\ngo 1.21\n\nrequire example.com/answer v0.0.0\n\nreplace example.com/answer => ../../../libraries/answer\n")
	writeFile(t, root, "checkout/services/local/code/main.go",
		"package main\n\nimport (\n\t\"fmt\"\n\t\"os\"\n\n\t\"example.com/answer\"\n)\n\n"+
			"func main() {\n\tfmt.Println(answer.Value())\n"+
			"\tif err := os.WriteFile(\"written-by-the-container\", nil, 0o644); err == nil {\n\t\tfmt.Println(\"source is writable\")\n\t}\n}\n")

	env, err := golang.NewDockerGoRunner(ctx,
		resources.NewDockerImage("golang:1.26-alpine"),
		workspace, "../checkout/services/local/code",
		fmt.Sprintf("test-composed-%d", time.Now().UnixMilli()))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, env.Shutdown(context.Background())) })
	env.WithLocalCacheDir(t.TempDir())
	env.WithGoModDir(t.TempDir())

	mounts, err := env.ContainerSourceMounts()
	require.NoError(t, err)
	require.Equal(t, []string{filepath.Join(root, "checkout", "libraries", "answer"), service}, mounts)

	require.NoError(t, env.Init(ctx))
	require.NoError(t, env.BuildBinary(ctx))
	require.Equal(t, "composed", runOutput(t, ctx, env))
	require.NoFileExists(t, filepath.Join(service, "written-by-the-container"))
}
