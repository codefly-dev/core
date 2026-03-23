package proto_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/codefly-dev/core/companions/proto"
	"github.com/codefly-dev/core/companions/testutil"
	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

func TestGenerateSwagger(t *testing.T) {
	ctx := context.Background()
	wool.SetGlobalLogLevel(wool.DEBUG)

	testutil.RequireProtoImage(t, ctx)

	_, filename, _, _ := runtime.Caller(0)
	testdata := filepath.Join(filepath.Dir(filename), "testdata")
	apiJSON := filepath.Join(testdata, "api.json")
	require.FileExists(t, apiJSON)

	ep := &resources.Endpoint{Module: "web", Service: "api", Name: "api", Visibility: "private"}
	rest, err := resources.LoadRestAPI(ctx, shared.Pointer(apiJSON))
	require.NoError(t, err)
	api, err := resources.NewAPI(ctx, ep, resources.ToRestAPI(rest))
	require.NoError(t, err)

	destination := filepath.Join(testdata, "openapi")
	defer os.RemoveAll(destination)

	err = proto.GenerateOpenAPI(ctx, languages.GO, destination, "web/api", api)
	if err != nil && strings.Contains(err.Error(), "No such image") {
		t.Skipf("proto companion image not built: %s (run ./companions/scripts/build_companions.sh from core/)", err)
	}
	require.NoError(t, err)

	for _, dir := range []string{"models", "client"} {
		require.DirExists(t, filepath.Join(destination, dir))
	}
}
