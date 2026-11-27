package golang_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/internal/testgit"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/golang"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/shared"
)

// testGo exercises a configured GoRunnerEnvironment end-to-end:
// initialize, build the binary, run it, observe output, stop. All
// callers run go-module-mode programs (Go 1.21+ defaults to module
// mode and the legacy GOPATH path was dropped — see
// TestDockerRunNoMod removal).
func testGo(t *testing.T, ctx context.Context, env *golang.GoRunnerEnvironment) {
	t.Helper()
	cacheDir := t.TempDir()
	env.WithLocalCacheDir(cacheDir)
	env.WithGoModDir(t.TempDir())
	t.Cleanup(func() { require.NoError(t, env.Shutdown(context.Background())) })

	// Init
	err := env.Init(ctx)
	require.NoError(t, err)

	err = env.BuildBinary(ctx)
	require.NoError(t, err)

	// Check that the binary is there
	require.False(t, shared.Must(shared.CheckEmptyDirectory(ctx, cacheDir)))

	// A second build is the toolchain's own up-to-date check on that binary.
	err = env.BuildBinary(ctx)
	require.NoError(t, err)

	// Run and stop
	proc, err := env.Runner()
	require.NoError(t, err)

	data := shared.NewSliceWriter()
	output := shared.NewSignalWriter(data)
	proc.WithOutput(output)

	err = proc.Start(ctx)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		for _, line := range data.Snapshot() {
			if strings.Contains(line, "running") {
				return true
			}
		}
		return false
	}, 10*time.Second, 20*time.Millisecond, "runner did not report readiness")
	require.True(t, shared.Must(proc.IsRunning(ctx)))

	err = proc.Stop(ctx)
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		lines := data.Snapshot()
		return len(lines) > 0 && strings.Contains(lines[len(lines)-1], "signal received")
	}, 10*time.Second, 20*time.Millisecond, "runner did not report its stop")
	testOutput(t, data)

	require.False(t, shared.Must(proc.IsRunning(ctx)))
}

func testOutput(t *testing.T, data *shared.SliceWriter) {
	// Snapshot under lock — proc.Run returns when the user binary
	// exits, but the forwarder goroutine may still be flushing the
	// last buffered line. Reading Data directly races with Write.
	snap := data.Snapshot()
	require.True(t, len(snap) > 1)
	for _, line := range snap[:len(snap)-2] {
		require.Contains(t, line, "running")
		require.NotContains(t, line, "running\n")
	}
	require.Contains(t, snap[len(snap)-1], "signal received")
}

// TestGoModuleHandlingCacheAdvancesOnlyOnDownloadSuccess pins the ordering
// invariant: the dependency hash must be persisted only after `go mod
// download` succeeds. If it were advanced first, a failed download would be
// masked on the next run (Updated() == false → fetch skipped, modules
// missing). GOPROXY=off forces the subprocess offline so the failure path is
// deterministic and network-free.
func TestGoModuleHandlingCacheAdvancesOnlyOnDownloadSuccess(t *testing.T) {
	ctx := context.Background()
	t.Setenv("GOPROXY", "off")

	newEnv := func(t *testing.T, gomod string) (*golang.GoRunnerEnvironment, string) {
		t.Helper()
		root := t.TempDir()
		cacheDir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte(gomod), 0644))
		require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0644))
		env, err := golang.NewNativeGoRunner(ctx, root, ".")
		require.NoError(t, err)
		env.WithLocalCacheDir(cacheDir)
		env.Setup(ctx)
		return env, filepath.Join(cacheDir, "native", "gomod.hash")
	}

	t.Run("failure leaves cache unadvanced", func(t *testing.T) {
		// Requires a module that cannot be resolved offline, so `go mod
		// download` fails.
		env, hashFile := newEnv(t, "module example.com/faildl\n\ngo 1.21\n\nrequire example.com/does-not-exist v0.0.1\n")
		defer func() { require.NoError(t, env.Shutdown(ctx)) }()

		require.Error(t, env.GoModuleHandling(ctx))

		_, statErr := os.Stat(hashFile)
		require.True(t, os.IsNotExist(statErr), "cache hash was advanced despite failed download")
	})

	t.Run("success advances cache", func(t *testing.T) {
		// No external requires: `go mod download` succeeds even offline.
		env, hashFile := newEnv(t, "module example.com/okdl\n\ngo 1.21\n")
		defer func() { require.NoError(t, env.Shutdown(ctx)) }()

		require.NoError(t, env.GoModuleHandling(ctx))

		_, statErr := os.Stat(hashFile)
		require.NoError(t, statErr, "cache hash was not persisted after successful download")
	})
}

func TestGoModuleHandlingPreservesParserDiagnostics(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte("modle example.com/broken\n\ngo 1.21\n"), 0o644))
	env, err := golang.NewNativeGoRunner(ctx, root, ".")
	require.NoError(t, err)
	env.WithLocalCacheDir(t.TempDir())
	t.Cleanup(func() { require.NoError(t, env.Shutdown(context.Background())) })

	err = env.GoModuleHandling(ctx)
	require.Error(t, err)
	require.Contains(t, err.Error(), "errors parsing go.mod")
	require.Contains(t, err.Error(), "unknown directive: modle")
}

func TestGoRunnerInitDoesNotResolveOrMutateProjectDependencies(t *testing.T) {
	ctx := context.Background()
	t.Setenv("GOPROXY", "off")
	root := t.TempDir()
	goMod := "module example.com/read-only-init\n\ngo 1.21\n\nrequire example.com/does-not-exist v0.0.1\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"), []byte(goMod), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() {}\n"), 0o644))
	env, err := golang.NewNativeGoRunner(ctx, root, ".")
	require.NoError(t, err)
	env.WithLocalCacheDir(t.TempDir())
	t.Cleanup(func() { require.NoError(t, env.Shutdown(context.Background())) })

	require.NoError(t, env.Init(ctx), "environment initialization must not resolve project dependencies")
	_, statErr := os.Stat(filepath.Join(root, "go.sum"))
	require.True(t, os.IsNotExist(statErr), "environment initialization created go.sum: %v", statErr)
}

// Each backend gets its own workspace so builders can run concurrently
// without sharing a mutable Go module or a Nix flake checkout.
func copyRunnerTestdata(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.CopyFS(dir, os.DirFS("testdata")))
	return dir
}

func TestNativeRunWithMod(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env, err := golang.NewNativeGoRunner(ctx, copyRunnerTestdata(t), "mod")
	require.NoError(t, err)
	testGo(t, ctx, env)
}

// A package the main imports is a compiled input: changing it rebuilds the
// executable, read from the toolchain's own trace and from the executable's
// output, never from a flag the runner sets.
func TestNativeRunRebuildsOnImportedPackageChange(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/cachetest\n\ngo 1.21\n")
	writeFile(t, root, "cmd/server/main.go", `package main

import (
	"fmt"

	"example.com/cachetest/pkg/answer"
)

func main() {
	fmt.Println(answer.Value())
}
`)
	answer := func(value string) {
		writeFile(t, root, "pkg/answer/answer.go", "package answer\n\nfunc Value() string { return \""+value+"\" }\n")
	}
	answer("one")
	env := newNativeRunner(t, ctx, root, "cmd/server", "-x")

	require.True(t, buildWithEvidence(t, ctx, env).link)
	require.Equal(t, "one", runOutput(t, ctx, env))
	require.Equal(t, reused, buildWithEvidence(t, ctx, env), "nothing changed")

	answer("two")
	require.True(t, buildWithEvidence(t, ctx, env).link, "the changed package relinks the executable")
	require.Equal(t, "two", runOutput(t, ctx, env))
}

func TestNativeRunWithModAndCGO(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	env, err := golang.NewNativeGoRunner(ctx, copyRunnerTestdata(t), "mod_cgo")
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

	t.Parallel()
	ctx := t.Context()

	// Copy testdata to a temp dir so nix can see the flake.nix
	// (nix flakes only see git-tracked files, and testdata/flake.nix
	// may not be tracked in core's repo)
	tmpDir := copyRunnerTestdata(t)

	// Initialize a git repo so nix can see the flake
	for _, args := range [][]string{{"init"}, {"add", "-A"}} {
		out, err := testgit.Run(t.Context(), tmpDir, nil, args...)
		require.NoError(t, err, "%s", out)
	}

	env, err := golang.NewNixGoRunner(ctx, tmpDir, "mod")
	require.NoError(t, err)
	testGo(t, ctx, env)
}

func TestDockerRunWithMod(t *testing.T) {
	ctx := t.Context()
	name := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	env, err := golang.NewDockerGoRunner(ctx,
		resources.NewDockerImage("golang:1.26-alpine"),
		copyRunnerTestdata(t), "mod",
		name)
	require.NoError(t, err)

	testGo(t, ctx, env)
}

func TestDockerRunWithModAndCGO(t *testing.T) {
	ctx := t.Context()
	name := fmt.Sprintf("%s-%d", t.Name(), time.Now().UnixNano())
	env, err := golang.NewDockerGoRunner(ctx,
		resources.NewDockerImage("golang:1.26"),
		copyRunnerTestdata(t), "mod_cgo",
		name)
	require.NoError(t, err)

	env.WithCGO(true)
	testGo(t, ctx, env)
}

// A service whose go.mod replaces a module with a local directory compiles that
// directory into its binary: a change there must rebuild, or the service runs
// code older than its sources. The toolchain sees the replacement as it sees
// any other compiled input; the runner parses no go.mod to find it.
func TestNativeRunRebuildsOnLocallyReplacedModuleChange(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeFile(t, root, "lib/go.mod", "module example.com/lib\n\ngo 1.21\n")
	answer := func(value string) {
		writeFile(t, root, "lib/answer/answer.go", "package answer\n\nfunc Value() string { return \""+value+"\" }\n")
	}
	answer("one")
	writeFile(t, root, "service/go.mod", "module example.com/service\n\ngo 1.21\n\nrequire example.com/lib v0.0.0\n\nreplace example.com/lib => ../lib\n")
	writeFile(t, root, "service/main.go", "package main\n\nimport (\n\t\"fmt\"\n\n\t\"example.com/lib/answer\"\n)\n\nfunc main() { fmt.Println(answer.Value()) }\n")

	env := newNativeRunner(t, ctx, filepath.Join(root, "service"), ".", "-x")

	require.True(t, buildWithEvidence(t, ctx, env).link)
	require.Equal(t, "one", runOutput(t, ctx, env))
	require.Equal(t, reused, buildWithEvidence(t, ctx, env), "nothing changed")

	answer("two")
	require.True(t, buildWithEvidence(t, ctx, env).link, "the replaced module changed, so the executable is relinked")
	require.Equal(t, "two", runOutput(t, ctx, env))
}
