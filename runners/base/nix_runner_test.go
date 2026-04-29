//go:build !skip_infra

package base_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/runners/base"
	"os"
	"path/filepath"

	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

func requireNix(t *testing.T) {
	t.Helper()
	if !base.CheckNixInstalled() {
		t.Fatal("nix is not installed; install Nix or run with -tags skip_infra to exclude")
	}
}

func nixTestDir(t *testing.T) string {
	t.Helper()
	// Go test runs with cwd = package directory
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(cwd, "testdata")
}

func TestNixEnvironment(t *testing.T) {
	requireNix(t)
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	dir := nixTestDir(t)
	env, err := base.NewNixEnvironment(ctx, dir)
	require.NoError(t, err, "nix environment not usable")

	err = env.Init(ctx)
	require.NoError(t, err)

	// WithBinary is a no-op in nix (binaries come from flake)
	err = env.WithBinary("ls")
	require.NoError(t, err)

	// Run a simple command inside nix develop
	proc, err := env.NewProcess("ls")
	require.NoError(t, err)

	d := shared.NewSliceWriter()
	output := shared.NewSignalWriter(d)
	proc.WithOutput(output)

	require.NoError(t, proc.Run(ctx), "nix develop failed (nix version may be too old)")

	// Should see testdata contents
	require.Contains(t, d.Data, "good")
	require.Contains(t, d.Data, "crashing")
}

func TestNixEnvironment_FiniteScript(t *testing.T) {
	requireNix(t)
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	dir := nixTestDir(t)
	env, err := base.NewNixEnvironment(ctx, dir)
	require.NoError(t, err, "nix environment not usable")

	err = env.Init(ctx)
	require.NoError(t, err)

	proc, err := env.NewProcess("sh", "good/finite_counter.sh")
	require.NoError(t, err)

	d := shared.NewSliceWriter()
	output := shared.NewSignalWriter(d)
	proc.WithOutput(output)

	require.NoError(t, proc.Run(ctx), "nix develop failed (nix version may be too old)")
	require.Contains(t, d.Data, "1")
}

func TestNixEnvironment_NoFlake(t *testing.T) {
	requireNix(t)
	ctx := context.Background()

	// Directory without flake.nix should fail
	_, err := base.NewNixEnvironment(ctx, "/tmp")
	require.Error(t, err)
	require.Contains(t, err.Error(), "flake.nix")
}
