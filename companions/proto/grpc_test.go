package proto_test

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/companions/proto"
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/configurations/languages"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
)

func TestGenerateGRPC(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	// Load some endpoints
	ctx := context.Background()
	f, err := shared.SolvePath("testdata/api.proto")
	assert.NoError(t, err)
	ep := &configurations.Endpoint{Application: "app", Service: "svc", Name: "api", Visibility: "private"}
	api, err := configurations.LoadGrpcAPI(ctx, shared.Pointer(f))
	assert.NoError(t, err)
	grpc, err := configurations.NewAPI(ctx, ep, configurations.ToGrpcAPI(api))
	assert.NoError(t, err)
	destination := t.TempDir()
	defer os.RemoveAll(destination)
	err = proto.GenerateGRPC(ctx, languages.GO, destination, "app/svc", grpc)
	assert.NoError(t, err)
	// Make sure we have the files
	files := []string{"app_svc_api.pb.go", "app_svc_api_grpc.pb.go"}
	for _, f := range files {
		p := path.Join(destination, f)
		assert.NoError(t, err)
		assert.FileExists(t, p)
	}
}
