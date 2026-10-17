package golang_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
