//go:build flaky

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
	dir, err := shared.SolvePath("testdata/swagger.json")
	assert.NoError(t, err)
	rest, err := configurations.NewRestAPIFromOpenAPI(ctx, &configurations.Endpoint{Application: "web", Service: "api", Name: "api", Visibility: "private"}, dir)
	assert.NoError(t, err)

	// Destination needs to be inside this package
	destination, err := shared.SolvePath("testdata")
	assert.NoError(t, err)
	destination = path.Join(destination, "swagger")

	defer os.RemoveAll(destination)
	err = proto.GenerateOpenAPI(ctx, languages.GO, destination, "web/api", rest)
	assert.NoError(t, err)

	// Make sure we have the dirs
	dirs := []string{"models", "client"}
	for _, f := range dirs {
		p := path.Join(destination, f)
		assert.NoError(t, err)
		assert.DirExists(t, p)
	}
}
