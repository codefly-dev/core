//go:build python_companion_required

package dap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/codefly-dev/core/companions/python"
	"github.com/codefly-dev/core/companions/testutil"
	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/runners/companion"
	"github.com/stretchr/testify/require"
)

func TestPythonCompanionInitializesAndLaunchesWithoutPip(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	testutil.RequireDocker(t, ctx)
	img, err := python.CompanionImage(ctx)
	require.NoError(t, err)
	out, err := exec.CommandContext(ctx, "docker", "image", "inspect", img.FullName()).CombinedOutput()
	require.NoError(t, err, "build the selected companion with codefly companion build python: %s", out)
	source := t.TempDir()
	program, err := os.ReadFile("testdata/python/main.py")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(source, "main.py"), program, 0o644))
	client, err := NewClient(ctx, languages.PYTHON, source)
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		require.NoError(t, client.Close(cleanup))
	})
	require.Equal(t, companion.BackendDocker, client.(*companionClient).runner.Backend())
	terminated := make(chan struct{}, 1)
	client.OnTerminated(func() {
		select {
		case terminated <- struct{}{}:
		default:
		}
	})
	require.NoError(t, client.Launch(ctx, "/workspace/main.py", nil, nil))
	select {
	case <-terminated:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	result, err := os.ReadFile(filepath.Join(source, "result.txt"))
	require.NoError(t, err)
	require.Equal(t, "debugpy without pip", string(result))
}
