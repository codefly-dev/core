package golang_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/runners/golang"
	"github.com/stretchr/testify/require"
)

// HostExecutable is what an agent reports it started
// (runtime-started-artifact/v1). These tests read the answer against a real
// native build: the file it names has to be the program Runner() runs, and
// there has to be no answer at all when no build has succeeded — a consumer
// that digests the executable approves a build, so a path to the wrong file or
// to no file is worse than a refusal.

// Nothing has been built, so there is nothing running to describe. Runner()
// refuses for the same reason and from the same field.
func TestHostExecutableIsUnreportedBeforeAnyBuild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/unbuilt\n\ngo 1.21\n")
	writeFile(t, root, "main.go", "package main\n\nfunc main() {}\n")
	env := newNativeRunner(t, ctx, root, ".", "")

	executable, reported := env.HostExecutable()
	require.False(t, reported, "no build has succeeded, so no executable is running")
	require.Empty(t, executable)

	_, err := env.Runner()
	require.Error(t, err, "Runner refuses from the same state")
}

// The reported path is the program that runs: running the file at that path
// prints what the runner's own process prints. The agent hands this to a
// consumer that digests the file, so "what it reports is what ran" is the
// whole contract.
func TestHostExecutableNamesTheProgramRunnerRuns(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/reported\n\ngo 1.21\n")
	writeFile(t, root, "main.go", `package main

import "fmt"

func main() { fmt.Print("first-build") }
`)
	env := newNativeRunner(t, ctx, root, ".", "")
	require.NoError(t, env.BuildBinary(ctx))

	executable, reported := env.HostExecutable()
	require.True(t, reported, "a native build's executable is a file on this host")
	require.True(t, filepath.IsAbs(executable), "the contract reports absolute paths, got %q", executable)
	info, err := os.Stat(executable)
	require.NoError(t, err, "the reported path is a file the host can open")
	require.False(t, info.IsDir())

	require.Equal(t, "first-build", runOutput(t, ctx, env))
	require.Equal(t, "first-build", directOutput(t, ctx, executable),
		"the reported file is the program Runner() runs, not a path beside it")

	// A rebuild describes the rebuilt program. The runner keeps one executable
	// per build environment, so the path is stable while its bytes change —
	// which is why a consumer digests the file rather than the path.
	writeFile(t, root, "main.go", `package main

import "fmt"

func main() { fmt.Print("second-build") }
`)
	require.NoError(t, env.BuildBinary(ctx))
	rebuilt, reported := env.HostExecutable()
	require.True(t, reported)
	require.Equal(t, executable, rebuilt)
	require.Equal(t, "second-build", runOutput(t, ctx, env))
	require.Equal(t, "second-build", directOutput(t, ctx, rebuilt))
}

// A build that failed left nothing new to run, and the previous executable is
// not this attempt's answer: an agent that reported it would have a consumer
// approve a build the failed start never reached.
func TestHostExecutableIsUnreportedAfterAFailedBuild(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/failed\n\ngo 1.21\n")
	writeFile(t, root, "main.go", `package main

import "fmt"

func main() { fmt.Print("compiles") }
`)
	env := newNativeRunner(t, ctx, root, ".", "")
	require.NoError(t, env.BuildBinary(ctx))
	built, reported := env.HostExecutable()
	require.True(t, reported)

	writeFile(t, root, "main.go", "package main\n\nfunc main() { this is not go }\n")
	require.Error(t, env.BuildBinary(ctx), "the source does not compile")

	executable, reported := env.HostExecutable()
	require.False(t, reported, "the failed attempt reports nothing, not the previous executable")
	require.Empty(t, executable)
	require.FileExists(t, built, "the previous executable is still on disk; it is simply not this attempt's answer")
}

// directOutput runs the executable at path itself, through os/exec and
// nothing of the runner's, and returns what it printed. It is the independent
// witness that the reported path holds the program the runner ran.
func directOutput(t *testing.T, ctx context.Context, path string) string {
	t.Helper()
	out, err := exec.CommandContext(ctx, path).Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

// The reported path is absolute even when nothing the caller gave was. The
// runner's directory and cache location come from the caller, and a relative
// one leaves `go build -o` writing a relative target. The contract names
// absolute paths and a consumer refuses a relative one rather than resolving it
// against a user's terminal, so the resolution happens here — against
// sourceDir, which is the directory the build process ran in and therefore
// where the file actually is.
//
// This also pins WHICH resolution is right, because the two candidates differ:
// the build writes relative to sourceDir, while this process's working
// directory is something else entirely. Resolving against the latter would name
// a file that is not there.
func TestHostExecutableIsAbsoluteFromARelativeRunnerDirectory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	writeFile(t, root, "service/go.mod", "module example.com/relative\n\ngo 1.21\n")
	writeFile(t, root, "service/main.go", `package main

import "fmt"

func main() { fmt.Print("relative-build") }
`)

	// Drive the runner entirely through relative paths, from root — so the
	// build's directory (root/service) and this process's (root) differ.
	restore := chdir(t, root)
	defer restore()

	env, err := golang.NewNativeGoRunner(ctx, "service", ".")
	require.NoError(t, err)
	env.WithLocalCacheDir("cache")
	t.Cleanup(func() { require.NoError(t, env.Shutdown(context.Background())) })
	require.NoError(t, env.Init(ctx))
	require.NoError(t, env.BuildBinary(ctx))

	executable, reported := env.HostExecutable()
	require.True(t, reported)
	require.True(t, filepath.IsAbs(executable),
		"reported %q, which the consumer refuses as not absolute", executable)
	require.FileExists(t, executable,
		"the absolute path must name the file the build actually wrote")

	// The build wrote under sourceDir, not under this process's directory.
	// Naming the wrong one would pass IsAbs and still be a file that is not
	// there, which is the failure the consumer digests rather than refuses.
	require.Equal(t, mustAbs(t, filepath.Join(root, "service", "cache", "native", binaryUnderTest)), resolved(t, executable))
	require.NoFileExists(t, filepath.Join(root, "cache", "native", binaryUnderTest),
		"nothing was written under this process's directory, so resolving there would name a missing file")

	// It is the program: both the runner's own process and that file run alone
	// print what the service prints.
	require.Equal(t, "relative-build", runOutput(t, ctx, env))
	require.Equal(t, "relative-build", directOutput(t, ctx, executable))

	// And it stays correct once this process's own directory moves, which a
	// path resolved against the working directory would not.
	restore()
	again, reported := env.HostExecutable()
	require.True(t, reported)
	require.Equal(t, executable, again)
	require.FileExists(t, again)
}

// binaryUnderTest is the name the runner gives its one executable. Spelled here
// rather than imported because this is the external test package; the assertion
// that uses it is about WHERE the file is, not what it is called.
const binaryUnderTest = "main"

// A relative cache location still runs, and runs the file that is reported.
// BuildBinary records the target absolute, so Runner() — which hands the path
// to exec.LookPath, resolved against THIS process rather than sourceDir —
// reaches the same file the build wrote. Before that, a relative location could
// not run a service at all: the build wrote under sourceDir and the exec looked
// under the process's own directory.
func TestRelativeCacheLocationRunsTheFileItReports(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	writeFile(t, root, "service/go.mod", "module example.com/relative-runner\n\ngo 1.21\n")
	writeFile(t, root, "service/main.go", `package main

import "fmt"

func main() { fmt.Print("ran-from-relative") }
`)
	restore := chdir(t, root)
	defer restore()

	env, err := golang.NewNativeGoRunner(ctx, "service", ".")
	require.NoError(t, err)
	env.WithLocalCacheDir("cache")
	t.Cleanup(func() { require.NoError(t, env.Shutdown(context.Background())) })
	require.NoError(t, env.Init(ctx))
	require.NoError(t, env.BuildBinary(ctx))

	executable, reported := env.HostExecutable()
	require.True(t, reported)
	require.FileExists(t, executable)

	// Runner() finds it, and it is the same program the report names.
	require.Equal(t, "ran-from-relative", runOutput(t, ctx, env))
	require.Equal(t, "ran-from-relative", directOutput(t, ctx, executable))
}

// chdir moves this process into dir and returns a function restoring the
// previous directory; calling it twice is harmless.
func chdir(t *testing.T, dir string) func() {
	t.Helper()
	previous, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	restored := false
	return func() {
		if restored {
			return
		}
		restored = true
		require.NoError(t, os.Chdir(previous))
	}
}

// resolved follows symlinks so a path reached as /var and reported as
// /private/var compares equal; t.TempDir hands out the former on darwin.
func resolved(t *testing.T, path string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)
	return real
}

func mustAbs(t *testing.T, path string) string {
	t.Helper()
	return resolved(t, path)
}
