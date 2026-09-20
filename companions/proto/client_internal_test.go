package proto

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/codefly-dev/core/languages"
	"github.com/gofrs/flock"
	"github.com/stretchr/testify/require"
)

func writeRustOutput(t *testing.T, dir, name, content string) {
	t.Helper()
	file := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o750))
	require.NoError(t, os.WriteFile(file, []byte(content), 0o600))
}

func TestCanceledRustGenerationPreservesDestination(t *testing.T) {
	dest := t.TempDir()
	for _, name := range []string{"api/api.rs", "src/user.rs", "google/rpc/google.rpc.rs"} {
		writeRustOutput(t, dest, name, "existing content")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := GenerateClient(ctx, ClientRequest{Language: languages.RUST, Destination: dest,
		Sources: []Source{{Path: "api.proto", Content: []byte(`syntax = "proto3"; package api; message Response { string value = 1; }`)}}})
	require.Error(t, err)
	for _, name := range []string{"api/api.rs", "src/user.rs", "google/rpc/google.rpc.rs"} {
		data, err := os.ReadFile(filepath.Join(dest, name))
		require.NoError(t, err)
		require.Equal(t, "existing content", string(data))
	}
}

func TestRustPublicationOwnsFilesNotDirectories(t *testing.T) {
	dest := t.TempDir()
	writeRustOutput(t, dest, "src/user.rs", "user content")
	first := t.TempDir()
	writeRustOutput(t, first, "api/api.rs", "old generated")
	writeRustOutput(t, first, "google/rpc/google.rpc.rs", "old import")
	require.NoError(t, publishRustOutput(dest, first))
	writeRustOutput(t, dest, "google/rpc/user.rs", "user content")
	second := t.TempDir()
	writeRustOutput(t, second, "api/api.rs", "new generated")
	require.NoError(t, publishRustOutput(dest, second))
	require.NoFileExists(t, filepath.Join(dest, "google/rpc/google.rpc.rs"))
	for _, name := range []string{"src/user.rs", "google/rpc/user.rs"} {
		data, err := os.ReadFile(filepath.Join(dest, name))
		require.NoError(t, err)
		require.Equal(t, "user content", string(data))
	}
	data, err := os.ReadFile(filepath.Join(dest, "api/api.rs"))
	require.NoError(t, err)
	require.Equal(t, "new generated", string(data))
}

func TestRustPublicationRejectsEditsAndUnownedCollisions(t *testing.T) {
	for _, owned := range []bool{false, true} {
		t.Run(fmt.Sprint(owned), func(t *testing.T) {
			dest := t.TempDir()
			if owned {
				first := t.TempDir()
				writeRustOutput(t, first, "api/api.rs", "generated")
				require.NoError(t, publishRustOutput(dest, first))
			}
			writeRustOutput(t, dest, "api/api.rs", "user edit")
			stage := t.TempDir()
			writeRustOutput(t, stage, "api/api.rs", "replacement")
			writeRustOutput(t, stage, "aaa/new.rs", "new output")
			require.ErrorContains(t, publishRustOutput(dest, stage), "unowned or edited")
			data, err := os.ReadFile(filepath.Join(dest, "api/api.rs"))
			require.NoError(t, err)
			require.Equal(t, "user edit", string(data))
			require.NoFileExists(t, filepath.Join(dest, "aaa/new.rs"))
		})
	}
}

func TestRustPublicationDoesNotPruneEditedImports(t *testing.T) {
	dest, first := t.TempDir(), t.TempDir()
	writeRustOutput(t, first, "google/rpc/google.rpc.rs", "generated import")
	require.NoError(t, publishRustOutput(dest, first))
	writeRustOutput(t, dest, "google/rpc/google.rpc.rs", "edited import")
	require.ErrorContains(t, publishRustOutput(dest, t.TempDir()), "unowned or edited")
	data, err := os.ReadFile(filepath.Join(dest, "google/rpc/google.rpc.rs"))
	require.NoError(t, err)
	require.Equal(t, "edited import", string(data))
}

func TestRustPublicationRollsBackPartialFailure(t *testing.T) {
	dest := t.TempDir()
	first := t.TempDir()
	writeRustOutput(t, first, "aaa/old.rs", "old output")
	require.NoError(t, publishRustOutput(dest, first))
	manifest, err := os.ReadFile(filepath.Join(dest, rustOutputManifest))
	require.NoError(t, err)
	require.NoError(t, os.Symlink("missing-target", filepath.Join(dest, "zzz")))
	stage := t.TempDir()
	writeRustOutput(t, stage, "aaa/old.rs", "replacement")
	writeRustOutput(t, stage, "zzz/new.rs", "cannot publish")
	require.Error(t, publishRustOutput(dest, stage))
	data, err := os.ReadFile(filepath.Join(dest, "aaa/old.rs"))
	require.NoError(t, err)
	require.Equal(t, "old output", string(data))
	data, err = os.ReadFile(filepath.Join(dest, rustOutputManifest))
	require.NoError(t, err)
	require.Equal(t, manifest, data)
	require.NoFileExists(t, filepath.Join(dest, "zzz/new.rs"))
}

func TestRustPublicationRejectsEscapingSymlink(t *testing.T) {
	dest, stage, outside := t.TempDir(), t.TempDir(), t.TempDir()
	require.NoError(t, os.Symlink(outside, filepath.Join(dest, "api")))
	writeRustOutput(t, stage, "api/api.rs", "generated")
	require.Error(t, publishRustOutput(dest, stage))
	require.NoFileExists(t, filepath.Join(outside, "api.rs"))
}

func TestRustGenerationHonorsDestinationLockThroughSymlink(t *testing.T) {
	dest, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	writeRustOutput(t, dest, "api/api.rs", "existing output")
	alias := filepath.Join(t.TempDir(), "alias")
	require.NoError(t, os.Symlink(dest, alias))
	lock := flock.New(filepath.Join(dest, rustOutputLock))
	require.NoError(t, lock.Lock())
	t.Cleanup(func() { require.NoError(t, lock.Close()) })
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err = GenerateClient(ctx, ClientRequest{Language: languages.RUST, Destination: alias,
		Sources: []Source{{Path: "api.proto", Content: []byte(`syntax = "proto3"; package api; message Response {}`)}}})
	require.ErrorIs(t, err, context.DeadlineExceeded)
	data, err := os.ReadFile(filepath.Join(dest, "api/api.rs"))
	require.NoError(t, err)
	require.Equal(t, "existing output", string(data))
}

func TestRustPublicationRejectsInvalidOwnership(t *testing.T) {
	for _, body := range []string{`{`, `{"../outside":"hash"}`, `{"/outside":"hash"}`, `{".codefly-rust-output.json":"hash"}`, `{".codefly-rust-output.lock":"hash"}`} {
		dest, stage := t.TempDir(), t.TempDir()
		writeRustOutput(t, dest, rustOutputManifest, body)
		writeRustOutput(t, stage, "api/api.rs", "generated")
		require.Error(t, publishRustOutput(dest, stage))
		require.NoFileExists(t, filepath.Join(dest, "api/api.rs"))
	}
}

// TestRemoveUnownedOutput pins what a TypeScript run is allowed to keep in its
// destination. Asking buf for the imports means the run writes a tree per
// imported namespace, which is not a set known ahead of time — so the
// destination is reclaimed by what this run owns, and a namespace the contract
// stopped importing cannot survive into the library.
func TestRemoveUnownedOutput(t *testing.T) {
	dest := t.TempDir()
	for _, rel := range []string{
		"acme/health/v1/health_pb.ts",
		"grpc/health/v1/health_pb.ts",
		"google/api/annotations_pb.ts",
		"buf/validate/validate_pb.ts",
	} {
		path := filepath.Join(dest, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
		require.NoError(t, os.WriteFile(path, []byte("export {};\n"), 0600))
	}

	require.NoError(t, removeUnownedOutput(context.Background(), dest,
		[]Source{{Path: "acme/health/v1/health.proto"}}))

	require.FileExists(t, filepath.Join(dest, filepath.FromSlash("acme/health/v1/health_pb.ts")))
	for _, reclaimed := range []string{"grpc", "google", "buf"} {
		_, err := os.Stat(filepath.Join(dest, reclaimed))
		require.True(t, os.IsNotExist(err), "%s is not this run's to keep", reclaimed)
	}
}

// TestRemoveUnownedOutputKeepsAncestorsOfOwnedDirectories covers the case that
// makes reclaiming by namespace root wrong: a module whose own protos live
// under one of the roots the library otherwise never owns. buf/validate is
// reclaimed, buf/mymodule is the run's own output and must survive.
func TestRemoveUnownedOutputKeepsAncestorsOfOwnedDirectories(t *testing.T) {
	dest := t.TempDir()
	for _, rel := range []string{"buf/mymodule/v1/mine_pb.ts", "buf/validate/validate_pb.ts"} {
		path := filepath.Join(dest, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0750))
		require.NoError(t, os.WriteFile(path, []byte("export {};\n"), 0600))
	}

	require.NoError(t, removeUnownedOutput(context.Background(), dest,
		[]Source{{Path: "buf/mymodule/v1/mine.proto"}}))

	require.FileExists(t, filepath.Join(dest, filepath.FromSlash("buf/mymodule/v1/mine_pb.ts")))
	_, err := os.Stat(filepath.Join(dest, filepath.FromSlash("buf/validate")))
	require.True(t, os.IsNotExist(err), "buf/validate is not the module's even when it owns protos under buf/")
}

// TestRemoveUnownedOutputKeepsRootLevelOutput covers GenerateGRPC's shape: the
// sources are flat file names, so the run owns the destination root and every
// directory in it came from generating the imports.
func TestRemoveUnownedOutputKeepsRootLevelOutput(t *testing.T) {
	dest := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dest, "app_svc_api_pb.ts"), []byte("export {};\n"), 0600))
	require.NoError(t, os.MkdirAll(filepath.Join(dest, "google", "api"), 0750))

	require.NoError(t, removeUnownedOutput(context.Background(), dest,
		[]Source{{Path: "app_svc_api.proto"}}))

	require.FileExists(t, filepath.Join(dest, "app_svc_api_pb.ts"))
	_, err := os.Stat(filepath.Join(dest, "google"))
	require.True(t, os.IsNotExist(err))
}
