//go:build proto_companion_required

package proto_test

import (
	"context"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/companions/proto"
	"github.com/codefly-dev/core/companions/testutil"
	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

// testdataDir returns the path to companions/proto/testdata (works from any cwd).
func testdataDir(t *testing.T) string {
	_, filename, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(filename), "testdata")
	abs, err := filepath.Abs(dir)
	require.NoError(t, err)
	return abs
}

func TestGenerateGoGRPC(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	testutil.RequireProtoImage(t, ctx)

	apiProto := filepath.Join(testdataDir(t), "api.proto")
	ep := &resources.Endpoint{Module: "app", Service: "svc", Name: "api", Visibility: "private"}
	api, err := resources.LoadGrpcAPI(ctx, shared.Pointer(apiProto))
	require.NoError(t, err)
	grpc, err := resources.NewAPI(ctx, ep, resources.ToGrpcAPI(api))
	require.NoError(t, err)
	destination := t.TempDir()

	err = proto.GenerateGRPC(ctx, languages.GO, destination, "app/svc", grpc)
	require.NoError(t, err, "proto companion image not built: %s", testutil.BuildCompanionsHint)

	for _, name := range []string{"app_svc_api.pb.go", "app_svc_api_grpc.pb.go"} {
		require.FileExists(t, filepath.Join(destination, name))
	}
}

func TestGeneratePythonGRPC(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	testutil.RequireProtoImage(t, ctx)

	apiProto := filepath.Join(testdataDir(t), "api.proto")
	ep := &resources.Endpoint{Module: "app", Service: "svc", Name: "api", Visibility: "private"}
	api, err := resources.LoadGrpcAPI(ctx, shared.Pointer(apiProto))
	require.NoError(t, err)
	grpc, err := resources.NewAPI(ctx, ep, resources.ToGrpcAPI(api))
	require.NoError(t, err)
	destination := t.TempDir()

	err = proto.GenerateGRPC(ctx, languages.PYTHON, destination, "app/svc", grpc)
	require.NoError(t, err, "proto companion image not built: %s", testutil.BuildCompanionsHint)

	for _, name := range []string{"app_svc_api_pb2.py", "app_svc_api_pb2_grpc.py"} {
		require.FileExists(t, filepath.Join(destination, name))
	}
}
