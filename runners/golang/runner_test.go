package golang_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/golang"

	"github.com/codefly-dev/core/wool"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/shared"
)

// testGo exercises a configured GoRunnerEnvironment end-to-end:
// initialize, build the binary, run it, observe output, stop. All
// callers run go-module-mode programs (Go 1.21+ defaults to module
// mode and the legacy GOPATH path was dropped — see
// TestDockerRunNoMod removal).
func testGo(t *testing.T, ctx context.Context, env *golang.GoRunnerEnvironment) {

	cacheDir, _ := os.MkdirTemp("", "cache")
	env.WithLocalCacheDir(cacheDir)
	goModDir, _ := os.MkdirTemp("", "mod")
	env.WithGoModDir(goModDir)

	defer func() {
		err := env.Shutdown(ctx)
		require.NoError(t, err)
		err = os.RemoveAll(cacheDir)
		require.NoError(t, err)
	}()

	// Init
	err := env.Init(ctx)
	require.NoError(t, err)

	err = env.BuildBinary(ctx)
	require.NoError(t, err)

	// The go.mod resolution should have populated the module dir.
	require.False(t, shared.Must(shared.CheckEmptyDirectory(ctx, goModDir)))

	// Check that the binary is there
	require.False(t, shared.Must(shared.CheckEmptyDirectory(ctx, cacheDir)))

	require.False(t, env.UsedCache())

	err = env.BuildBinary(ctx)
	require.NoError(t, err)
	require.True(t, env.UsedCache())

	// Run and stop
	proc, err := env.Runner()
	require.NoError(t, err)

	data := shared.NewSliceWriter()
	output := shared.NewSignalWriter(data)
	proc.WithOutput(output)

	err = proc.Start(ctx)
	require.NoError(t, err)

	time.Sleep(1 * time.Second)
	require.True(t, shared.Must(proc.IsRunning(ctx)))

	err = proc.Stop(ctx)
	require.NoError(t, err)

	time.Sleep(2 * time.Second)

	testOutput(t, data)

	require.False(t, shared.Must(proc.IsRunning(ctx)))
}

func testOutput(t *testing.T, data *shared.SliceWriter) {
	// Data has been written to the output
	require.True(t, len(data.Data) > 1)
	for _, line := range data.Data[:len(data.Data)-2] {
		require.Contains(t, line, "running")
		require.NotContains(t, line, "running\n")
	}
	require.Contains(t, data.Data[len(data.Data)-1], "signal received")
}

func TestNativeRunWithMod(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	env, err := golang.NewNativeGoRunner(ctx, shared.MustSolvePath("testdata"), "mod")
	require.NoError(t, err)
	testGo(t, ctx, env)
}

func TestNativeRunWithModAndCGO(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	env, err := golang.NewNativeGoRunner(ctx, shared.MustSolvePath("testdata"), "mod_cgo")
	require.NoError(t, err)
	env.WithCGO(true)
	testGo(t, ctx, env)
}

// nixVersionAtLeast returns true if the installed nix is >= major.minor.
func nixVersionAtLeast(major, minor int) bool {
	out, err := exec.Command("nix", "--version").Output()
	if err != nil {
		return false
	}
	// Output: "nix (Nix) 2.11.0"
	parts := strings.Fields(strings.TrimSpace(string(out)))
	if len(parts) < 3 {
		return false
	}
	ver := strings.SplitN(parts[len(parts)-1], ".", 3)
	if len(ver) < 2 {
		return false
	}
	maj, err1 := strconv.Atoi(ver[0])
	min, err2 := strconv.Atoi(ver[1])
	if err1 != nil || err2 != nil {
		return false
	}
	return maj > major || (maj == major && min >= minor)
}

func TestNixRunWithMod(t *testing.T) {
	if !nixVersionAtLeast(2, 18) {
		t.Fatal("nix >= 2.18 required (current nixpkgs needs it); upgrade Nix or run with -tags skip_infra")
	}

	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	// Copy testdata to a temp dir so nix can see the flake.nix
	// (nix flakes only see git-tracked files, and testdata/flake.nix
	// may not be tracked in core's repo)
	tmpDir := t.TempDir()
	cpCmd := exec.Command("cp", "-r", shared.MustSolvePath("testdata")+"/.", tmpDir)
	require.NoError(t, cpCmd.Run())

	// Initialize a git repo so nix can see the flake
	cmd := exec.Command("git", "init")
	cmd.Dir = tmpDir
	require.NoError(t, cmd.Run())
	cmd = exec.Command("git", "add", "-A")
	cmd.Dir = tmpDir
	require.NoError(t, cmd.Run())

	env, err := golang.NewNixGoRunner(ctx, tmpDir, "mod")
	require.NoError(t, err)
	testGo(t, ctx, env)
}

func TestDockerRunWithMod(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	name := fmt.Sprintf("test-mod-%d", time.Now().UnixMilli())
	env, err := golang.NewDockerGoRunner(ctx,
		resources.NewDockerImage("golang:1.26-alpine"),
		shared.MustSolvePath("testdata"), "mod",
		name)
	require.NoError(t, err)

	testGo(t, ctx, env)

	err = env.Shutdown(ctx)
	require.NoError(t, err)
}

func TestDockerRunWithModAndCGO(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	name := fmt.Sprintf("test-mod-%d", time.Now().UnixMilli())
	env, err := golang.NewDockerGoRunner(ctx,
		resources.NewDockerImage("golang:1.26"),
		shared.MustSolvePath("testdata"), "mod_cgo",
		name)
	require.NoError(t, err)

	env.WithCGO(true)
	testGo(t, ctx, env)

	err = env.Shutdown(ctx)
	require.NoError(t, err)
}

