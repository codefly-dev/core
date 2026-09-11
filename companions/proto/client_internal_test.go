package proto

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

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
