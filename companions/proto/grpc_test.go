package proto_test

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/companions/proto"
	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/require"
)

func TestGenerateGoGRPC(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	// Load some endpoints
	ctx := context.Background()
	f, err := shared.SolvePath("testdata/api.proto")
	require.NoError(t, err)
	ep := &resources.Endpoint{Module: "app", Service: "svc", Name: "api", Visibility: "private"}
	api, err := resources.LoadGrpcAPI(ctx, shared.Pointer(f))
	require.NoError(t, err)
	grpc, err := resources.NewAPI(ctx, ep, resources.ToGrpcAPI(api))
	require.NoError(t, err)
	destination := t.TempDir()
	defer os.RemoveAll(destination)

	err = proto.GenerateGRPC(ctx, languages.GO, destination, "app/svc", grpc)
	require.NoError(t, err)
	// Make sure we have the files
	files := []string{"app_svc_api.pb.go", "app_svc_api_grpc.pb.go"}
	for _, f := range files {
		p := path.Join(destination, f)
		require.NoError(t, err)
		require.FileExists(t, p)
	}
}

func TestGeneratePythonGRPC(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	// Load some endpoints
	ctx := context.Background()
	f, err := shared.SolvePath("testdata/api.proto")
	require.NoError(t, err)
	ep := &resources.Endpoint{Module: "app", Service: "svc", Name: "api", Visibility: "private"}
	api, err := resources.LoadGrpcAPI(ctx, shared.Pointer(f))
	require.NoError(t, err)
	grpc, err := resources.NewAPI(ctx, ep, resources.ToGrpcAPI(api))
	require.NoError(t, err)
	destination := t.TempDir()
	defer os.RemoveAll(destination)

	err = proto.GenerateGRPC(ctx, languages.PYTHON, destination, "app/svc", grpc)
	require.NoError(t, err)

	files := []string{"app_svc_api_pb2.py", "app_svc_api_pb2_grpc.py"}
	for _, f := range files {
		p := path.Join(destination, f)
		require.NoError(t, err)
		require.FileExists(t, p)
	}
}
