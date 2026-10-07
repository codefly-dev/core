package golang_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/golang"
	"github.com/stretchr/testify/require"
)

// Runner offers an executable only if the build that produced it completed.
// Three ways a build can end without completing, each of which used to leave
// the previous executable published: module preparation failing before
// `go build` ran, `GOFLAGS=-n` turning `go build` into a dry run that exits 0
// and writes nothing, and a signal terminating the build mid-way.

func policyMain(t *testing.T, root, module string) {
	t.Helper()
	writeFile(t, root, "go.mod", "module example.com/"+module+"\n\ngo 1.21\n")
	writeFile(t, root, "main.go", `package main

import "fmt"

var policy = "unset"

func main() { fmt.Print(policy) }
`)
}

func TestNativeBuildRefusesThePreviousExecutableWhenModulePreparationFails(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	policyMain(t, root, "malformed-module")
	env := newNativeRunner(t, ctx, root, ".", "-x -ldflags=-X=main.policy=first")

	require.True(t, buildWithEvidence(t, ctx, env).link)
	require.Equal(t, "first", runOutput(t, ctx, env))

	writeFile(t, root, "go.mod", "module example.com/malformed-module\n\ngo 1.21\n\nrequire (\n") // unterminated block
	err := env.BuildBinary(ctx)
	require.Error(t, err, "module preparation fails before go build runs")
	require.Contains(t, err.Error(), "go modules")
	_, err = env.Runner()
	require.Error(t, err, "and the previous executable is not offered to run")

	writeFile(t, root, "go.mod", "module example.com/malformed-module\n\ngo 1.21\n")
	require.NoError(t, env.BuildBinary(ctx), "a repaired module builds again")
	require.Equal(t, "first", runOutput(t, ctx, env))
}

func TestNativeBuildIsNeverADryRun(t *testing.T) {
	policy := func(value string) string { return "-x -trimpath -ldflags=-X=main.policy=" + value }
	cases := []struct {
		name string
		set  func(t *testing.T, ctx context.Context, env *golang.GoRunnerEnvironment, goflags string)
	}{
		{"through the inherited environment", func(t *testing.T, _ context.Context, _ *golang.GoRunnerEnvironment, goflags string) {
			t.Setenv("GOFLAGS", goflags)
		}},
		{"through the runner's own variables", func(_ *testing.T, ctx context.Context, env *golang.GoRunnerEnvironment, goflags string) {
			env.WithEnvironmentVariables(ctx, resources.Env("GOFLAGS", goflags))
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			root := t.TempDir()
			policyMain(t, root, "dry-run")
			env := newNativeRunner(t, ctx, root, ".", "")

			tc.set(t, ctx, env, policy("first"))
			require.True(t, buildWithEvidence(t, ctx, env).link)
			require.Equal(t, "first", runOutput(t, ctx, env))

			tc.set(t, ctx, env, "-n "+policy("other"))
			require.NoError(t, env.BuildBinary(ctx))
			require.Equal(t, "other", runOutput(t, ctx, env),
				"GOFLAGS=-n must not turn the build into a dry run that leaves the previous executable")
		})
	}
}

func TestNativeBuildInterruptedBySignalPublishesNothing(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	root := t.TempDir()
	policyMain(t, root, "interrupted")
	env := newNativeRunner(t, ctx, root, ".", "-x -ldflags=-X=main.policy=first")

	require.True(t, buildWithEvidence(t, ctx, env).link)
	require.Equal(t, "first", runOutput(t, ctx, env))

	// The compiler is wrapped in a tool that terminates the go command
	// driving it: a real SIGTERM to a real build, after a change that makes
	// the build do work. The pause lets the signal land before the tool's
	// own exit could be reported in its place.
	interrupt := filepath.Join(t.TempDir(), "interrupt.sh")
	require.NoError(t, os.WriteFile(interrupt, []byte("#!/bin/sh\nkill -TERM $PPID\nsleep 2\nexit 1\n"), 0755))
	env.WithEnvironmentVariables(ctx, resources.Env("GOFLAGS", "-x -ldflags=-X=main.policy=second -toolexec="+interrupt))
	err := env.BuildBinary(ctx)
	require.Error(t, err, "an interrupted build is a failed build")
	require.Contains(t, err.Error(), "signal: terminated")
	_, err = env.Runner()
	require.Error(t, err, "and the previous executable is not offered to run")

	env.WithEnvironmentVariables(ctx, resources.Env("GOFLAGS", "-x -ldflags=-X=main.policy=second"))
	require.NoError(t, env.BuildBinary(ctx), "the next uninterrupted build recovers")
	require.Equal(t, "second", runOutput(t, ctx, env))
}
