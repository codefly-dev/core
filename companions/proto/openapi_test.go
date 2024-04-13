package proto_test

import (
	"context"
	"os"
	"path"
	"testing"

	"github.com/codefly-dev/core/companions/proto"
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/configurations/languages"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/assert"
)

func TestGenerateSwagger(t *testing.T) {
	// Load some endpoints
	ctx := context.Background()
	wool.SetGlobalLogLevel(wool.DEBUG)
	f, err := shared.SolvePath("testdata/api.json")
	assert.NoError(t, err)

	ep := &configurations.Endpoint{Application: "web", Service: "api", Name: "api", Visibility: "private"}
	rest, err := configurations.LoadRestAPI(ctx, shared.Pointer(f))
	assert.NoError(t, err)
	api, err := configurations.NewAPI(ctx, ep, configurations.ToRestAPI(rest))
	assert.NoError(t, err)

	// Destination needs to be inside this package
	destination, err := shared.SolvePath("testdata")
	assert.NoError(t, err)
	destination = path.Join(destination, "openapi")

	defer os.RemoveAll(destination)
	err = proto.GenerateOpenAPI(ctx, languages.GO, destination, "web/api", api)
	assert.NoError(t, err)

	// Make sure we have the dirs
	dirs := []string{"models", "client"}
	for _, f := range dirs {
		p := path.Join(destination, f)
		assert.NoError(t, err)
		assert.DirExists(t, p)
	}
}
