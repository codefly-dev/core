package golang_test

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/golang"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

// The runner keeps no executable cache of its own: every BuildBinary runs
// `go build -o` and the Go toolchain decides what to redo. These tests read
// that decision from the commands the toolchain prints under `-x` — the
// evidence is the toolchain's, never a flag the runner sets — and from what
// the executable prints when run.

// toolchainActions is what `go build -x` reported it ran for one BuildBinary.
type toolchainActions struct {
	compile bool // at least one package was compiled
	link    bool // the executable was linked
}

// reused is the toolchain finding nothing to do: no compile, no link.
var reused = toolchainActions{}

// toolRun matches a compile or link tool invocation in a `-x` trace, whatever
// the GOROOT and platform: `.../pkg/tool/darwin_arm64/link -o ...`.
var toolRun = regexp.MustCompile(`/tool/[^/ ]+/(compile|link) `)

// buildWithEvidence runs BuildBinary with the toolchain's command trace on the
// runner's output and reports which actions the trace shows. GOFLAGS must
// carry -x; newNativeRunner sets it with whatever flags the case needs.
func buildWithEvidence(t *testing.T, ctx context.Context, env *golang.GoRunnerEnvironment) toolchainActions {
	t.Helper()
	trace := shared.NewSliceWriter()
	env.WithOutput(trace)
	require.NoError(t, env.BuildBinary(ctx))
	var actions toolchainActions
	for _, line := range trace.Snapshot() {
		switch m := toolRun.FindStringSubmatch(line); {
		case m == nil:
		case m[1] == "compile":
			actions.compile = true
		case m[1] == "link":
			actions.link = true
		}
	}
	return actions
}

// runOutput runs the built executable to completion and returns what it
// printed.
func runOutput(t *testing.T, ctx context.Context, env *golang.GoRunnerEnvironment) string {
	t.Helper()
	proc, err := env.Runner()
	require.NoError(t, err)
	out := shared.NewSliceWriter()
	proc.WithOutput(out)
	require.NoError(t, proc.Run(ctx))
	return strings.TrimSpace(strings.Join(out.Snapshot(), "\n"))
}

func writeFile(t *testing.T, root, name, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(body), 0644))
}

// newNativeRunner is an initialized native runner on root/source with its own
// cache directory and the given GOFLAGS (which must include -x for
// buildWithEvidence to see the toolchain's trace).
func newNativeRunner(t *testing.T, ctx context.Context, root, source, goflags string) *golang.GoRunnerEnvironment {
	t.Helper()
	env, err := golang.NewNativeGoRunner(ctx, root, source)
	require.NoError(t, err)
	env.WithLocalCacheDir(t.TempDir())
	env.WithEnvironmentVariables(ctx, resources.Env("GOFLAGS", goflags))
	t.Cleanup(func() { require.NoError(t, env.Shutdown(context.Background())) })
	require.NoError(t, env.Init(ctx))
	return env
}

// Linker flags belong to the link action, not to any package. Under -trimpath
// Go records no -ldflags in package metadata (go.dev/issue/52372), so when only
// `-X main.policy` changes every compiled package build ID stays the same: a
// reuse key built from those IDs plus a source hash served the executable that
// still printed the old value. The toolchain's own check of the executable it
// is asked to write relinks it — and recompiles nothing, because nothing a
// compiler reads changed.
func TestNativeBuildRelinksWhenOnlyLinkerFlagsChange(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/linker-flags\n\ngo 1.21\n")
	writeFile(t, root, "main.go", `package main

import "fmt"

var policy = "unset"

func main() { fmt.Print(policy) }
`)
	policy := func(value string) string { return "-x -trimpath -ldflags=-X=main.policy=" + value }
	env := newNativeRunner(t, ctx, root, ".", policy("first"))

	require.True(t, buildWithEvidence(t, ctx, env).link, "the first build links the executable")
	require.Equal(t, "first", runOutput(t, ctx, env))
	require.Equal(t, reused, buildWithEvidence(t, ctx, env), "nothing changed: the toolchain left the executable in place")
	require.Equal(t, "first", runOutput(t, ctx, env))

	env.WithEnvironmentVariables(ctx, resources.Env("GOFLAGS", policy("other")))
	actions := buildWithEvidence(t, ctx, env)
	require.Equal(t, "other", runOutput(t, ctx, env), "the executable carries the new linker value")
	require.Equal(t, toolchainActions{link: true}, actions,
		"only the link action changed: the executable is relinked and no package is recompiled")
	require.Equal(t, reused, buildWithEvidence(t, ctx, env), "nothing changed since the relink")
	require.Equal(t, "other", runOutput(t, ctx, env))
}

// An embedded file is a compiled input of its package: changing only its bytes
// changes that package's build ID, so the toolchain rebuilds the executable.
// The runner walks no *.go files to find that out, and a missing embed fails
// the build with nothing stale left to run.
func TestNativeBuildRecompilesWhenOnlyEmbeddedYAMLChanges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	writeFile(t, root, "go.mod", "module example.com/embedded-cache\n\ngo 1.21\n")
	writeFile(t, root, "cmd/server/main.go", `package main

import (
	"fmt"

	"example.com/embedded-cache/config"
)

func main() { fmt.Print(config.Text) }
`)
	writeFile(t, root, "config/config.go", `package config

import _ "embed"

//go:embed policy.yaml
var Text string
`)
	writeFile(t, root, "config/policy.yaml", "policy: first\n")
	env := newNativeRunner(t, ctx, root, "cmd/server", "-x")

	require.True(t, buildWithEvidence(t, ctx, env).link, "the first build links the executable")
	require.Equal(t, "policy: first", runOutput(t, ctx, env))
	require.Equal(t, reused, buildWithEvidence(t, ctx, env), "nothing changed")
	require.Equal(t, "policy: first", runOutput(t, ctx, env))

	writeFile(t, root, "config/policy.yaml", "policy: other\n") // same size: content, not mtime or size
	require.True(t, buildWithEvidence(t, ctx, env).link, "the changed bytes relink the executable")
	require.Equal(t, "policy: other", runOutput(t, ctx, env))
	require.Equal(t, reused, buildWithEvidence(t, ctx, env), "nothing changed since the rebuild")

	writeFile(t, root, "config/policy.yaml", "policy: first\n")
	require.True(t, buildWithEvidence(t, ctx, env).link, "the original bytes are a changed input again")
	require.Equal(t, "policy: first", runOutput(t, ctx, env))

	require.NoError(t, os.Remove(filepath.Join(root, "config/policy.yaml")))
	require.Error(t, env.BuildBinary(ctx), "a missing embedded input fails the build")
	_, err := env.Runner()
	require.Error(t, err, "and the previous executable is not offered to run")
}
