package rust_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/runners/rust"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

// minimal Cargo project: a binary with two passing unit tests.
const cargoToml = `[package]
name = "demo-svc"
version = "0.1.0"
edition = "2021"

[[bin]]
name = "demo-svc"
path = "src/main.rs"
`

const mainRs = `fn add(a: i32, b: i32) -> i32 { a + b }

fn main() {
    println!("{}", add(1, 2));
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn it_adds() { assert_eq!(add(1, 2), 3); }

    #[test]
    fn it_adds_zero() { assert_eq!(add(0, 0), 0); }
}
`

// writeProject lays out <root>/code/{Cargo.toml,src/main.rs}.
func writeProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	code := filepath.Join(root, "code")
	require.NoError(t, os.MkdirAll(filepath.Join(code, "src"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(code, "Cargo.toml"), []byte(cargoToml), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(code, "src", "main.rs"), []byte(mainRs), 0o644))
	return root
}

// TestCargoDependencyHandlingCacheAdvancesOnlyOnFetchSuccess pins the
// ordering invariant: the dependency hash must be persisted only after
// `cargo fetch` succeeds. If it were advanced first, a failed fetch would be
// masked on the next run (Updated() == false → fetch skipped, crates
// missing). CARGO_NET_OFFLINE=true forces the subprocess offline so the
// failure path is deterministic and network-free.
func TestCargoDependencyHandlingCacheAdvancesOnlyOnFetchSuccess(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo not available")
	}
	ctx := context.Background()
	t.Setenv("CARGO_NET_OFFLINE", "true")

	newEnv := func(t *testing.T, toml string) (*rust.RustRunnerEnvironment, string) {
		t.Helper()
		root := t.TempDir()
		code := filepath.Join(root, "code")
		require.NoError(t, os.MkdirAll(filepath.Join(code, "src"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(code, "Cargo.toml"), []byte(toml), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(code, "src", "main.rs"), []byte(mainRs), 0o644))
		cacheDir := filepath.Join(root, "cache")
		env, err := rust.NewNativeRustRunner(ctx, root, "code")
		require.NoError(t, err)
		env.WithLocalCacheDir(cacheDir)
		env.Setup(ctx)
		return env, filepath.Join(cacheDir, "native", "cargo.hash")
	}

	t.Run("failure leaves cache unadvanced", func(t *testing.T) {
		// A dependency that cannot be resolved offline makes `cargo fetch` fail.
		env, hashFile := newEnv(t, cargoToml+"\n[dependencies]\ndoes-not-exist-xyz = \"9999.0.0\"\n")
		defer func() { _ = env.Shutdown(ctx) }()

		require.Error(t, env.CargoDependencyHandling(ctx))

		_, statErr := os.Stat(hashFile)
		require.True(t, os.IsNotExist(statErr), "cache hash was advanced despite failed fetch")
	})

	t.Run("success advances cache", func(t *testing.T) {
		// No dependencies: `cargo fetch` succeeds even offline.
		env, hashFile := newEnv(t, cargoToml)
		defer func() { _ = env.Shutdown(ctx) }()

		require.NoError(t, env.CargoDependencyHandling(ctx))

		_, statErr := os.Stat(hashFile)
		require.NoError(t, statErr, "cache hash was not persisted after successful fetch")
	})
}

// TestNativeBuildAndTest exercises the native RustRunnerEnvironment
// end-to-end: init, build (with hashed-cache short-circuit on rebuild), and
// `cargo test` result parsing. Skipped when cargo is unavailable.
func TestNativeBuildAndTest(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo not available")
	}
	ctx := context.Background()
	root := writeProject(t)
	cacheDir := filepath.Join(root, "cache")

	env, err := rust.NewNativeRustRunner(ctx, root, "code")
	require.NoError(t, err)
	env.WithLocalCacheDir(cacheDir)
	defer func() { _ = env.Shutdown(ctx) }()

	require.NoError(t, env.Init(ctx))

	require.NoError(t, env.BuildBinary(ctx))
	require.False(t, env.UsedCache(), "first build should not be cached")
	require.False(t, shared.Must(shared.CheckEmptyDirectory(ctx, cacheDir)), "cache should hold the binary")

	// Rebuild with no source change → cache hit.
	require.NoError(t, env.BuildBinary(ctx))
	require.True(t, env.UsedCache(), "rebuild should hit the cache")

	// Run the tests.
	summary, err := rust.RunCargoTests(ctx, env, filepath.Join(root, "code"), nil)
	require.NoError(t, err)
	require.Equal(t, int32(2), summary.Passed, "two passing tests")
	require.Equal(t, int32(0), summary.Failed)
}
