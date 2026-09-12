//go:build proto_companion_required

package proto_test

import (
	"context"
	"os"
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

	// api.proto imports google/api/annotations.proto, and protoc-gen-go names
	// its package absolutely: a local copy would be a second registration of
	// the same file in the consumer's descriptor pool.
	_, err = os.Stat(filepath.Join(destination, "google"))
	require.True(t, os.IsNotExist(err), "Go bindings reference googleapis upstream, so nothing is generated for it")
}

func TestGenerateRustGRPC(t *testing.T) {
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

	err = proto.GenerateGRPC(ctx, languages.RUST, destination, "app/svc", grpc)
	require.NoError(t, err, "proto companion image not built: %s", testutil.BuildCompanionsHint)

	// prost + tonic emit per proto package ("api/"): messages in api.rs,
	// the tonic client in api.tonic.rs.
	for _, name := range []string{"api/api.rs", "api/api.tonic.rs"} {
		require.FileExists(t, filepath.Join(destination, name))
	}
}

// TestGenerateTypeScriptGRPC covers the namespaces protoc-gen-es imports by a
// path relative to the bindings rather than from @bufbuild/protobuf/wkt. buf
// generates nothing for a dependency module, so without being told otherwise it
// emits the module's bindings alone and every one of those imports dangles.
//
// Only tsc catches it: generation succeeds either way and what it writes is
// well-formed TypeScript that happens to import files nothing wrote.
func TestGenerateTypeScriptGRPC(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()

	testutil.RequireProtoImage(t, ctx)

	annotatedProto := filepath.Join(testdataDir(t), "annotated.proto")
	ep := &resources.Endpoint{Module: "app", Service: "svc", Name: "api", Visibility: "private"}
	api, err := resources.LoadGrpcAPI(ctx, shared.Pointer(annotatedProto))
	require.NoError(t, err)
	grpc, err := resources.NewAPI(ctx, ep, resources.ToGrpcAPI(api))
	require.NoError(t, err)
	destination := t.TempDir()

	err = proto.GenerateGRPC(ctx, languages.TYPESCRIPT, destination, "app/svc", grpc)
	require.NoError(t, err, "proto companion image not built: %s", testutil.BuildCompanionsHint)

	for _, name := range []string{
		"app_svc_api_pb.ts",
		"google/api/annotations_pb.ts",
		"buf/validate/validate_pb.ts",
	} {
		require.FileExists(t, filepath.Join(destination, filepath.FromSlash(name)),
			"the bindings import %s relatively, so the library has to own it", name)
	}

	_, err = os.Stat(filepath.Join(destination, "google", "protobuf"))
	require.True(t, os.IsNotExist(err),
		"the well-known types come from @bufbuild/protobuf/wkt; a local copy is one Timestamp the consumer's is not")

	bindings, err := os.ReadFile(filepath.Join(destination, "app_svc_api_pb.ts"))
	require.NoError(t, err)
	require.Contains(t, string(bindings), "@bufbuild/protobuf/wkt")

	tsc(t, ctx, destination)
}
