package proto_test

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/companions/proto"
	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

func TestGenerateSwagger(t *testing.T) {
	// Load some endpoints
	ctx := context.Background()
	wool.SetGlobalLogLevel(wool.DEBUG)
	f, err := shared.SolvePath("testdata/api.json")
	require.NoError(t, err)

	ep := &resources.Endpoint{Module: "web", Service: "api", Name: "api", Visibility: "private"}
	rest, err := resources.LoadRestAPI(ctx, shared.Pointer(f))
	require.NoError(t, err)
	api, err := resources.NewAPI(ctx, ep, resources.ToRestAPI(rest))
	require.NoError(t, err)

	// Destination needs to be inside this package
	destination, err := shared.SolvePath("testdata")
	require.NoError(t, err)
	destination = path.Join(destination, "openapi")

	defer os.RemoveAll(destination)

	err = proto.GenerateOpenAPI(ctx, languages.GO, destination, "web/api", api)
	require.NoError(t, err)

	// Make sure we have the dirs
	dirs := []string{"models", "client"}
	for _, f := range dirs {
		p := path.Join(destination, f)
		require.NoError(t, err)
		require.DirExists(t, p)
	}
}
