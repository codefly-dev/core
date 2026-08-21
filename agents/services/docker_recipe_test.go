package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/wool"
	"github.com/stretchr/testify/require"
)

func TestBuildDockerBuildPlanInventoriesRecipeTree(t *testing.T) {
	destination := t.TempDir()
	tree := map[string]string{
		"app/Dockerfile":        "FROM alpine\n",
		"app/dockerignore":      "code/node_modules\n",
		"migration/Dockerfile":  "FROM alpine\nCOPY builder/runtime-access.sql /\n",
		"migration/runtime.sql": "GRANT SELECT;\n",
	}
	for name, content := range tree {
		path := filepath.Join(destination, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	}
	recipes := []*builderv0.DockerBuildRecipe{
		{Name: "app", Dockerfile: "app/Dockerfile", Context: ".", Image: "repo/app:v1", Platforms: []string{"linux/amd64", "linux/arm64"}},
		{Name: "migration", Dockerfile: "migration/Dockerfile", Context: ".", Image: "repo/migration:v1", Platforms: []string{"linux/amd64"}},
	}

	plan, err := BuildDockerBuildPlan(destination, recipes)
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeContractVersion, plan.GetContractVersion())
	require.Len(t, plan.GetRecipes(), 2)
	require.Len(t, plan.GetFiles(), len(tree))

	// Inventory is sorted by path and carries the real digest of each file.
	previous := ""
	byPath := map[string]string{}
	for _, file := range plan.GetFiles() {
		require.Less(t, previous, file.GetPath(), "inventory must be sorted by path")
		previous = file.GetPath()
		byPath[file.GetPath()] = file.GetDigest()
	}
	for name, content := range tree {
		sum := sha256.Sum256([]byte(content))
		require.Equal(t, "sha256:"+hex.EncodeToString(sum[:]), byPath[name])
	}

	// The aggregate digest is a deterministic function of the inventory.
	again, err := BuildDockerBuildPlan(destination, recipes)
	require.NoError(t, err)
	require.Equal(t, plan.GetDigest(), again.GetDigest())

	// A caller verifies the emitted tree against the plan.
	require.NoError(t, VerifyDockerBuildPlan(destination, plan))

	// Verification fails when the tree drifts from the plan.
	require.NoError(t, os.WriteFile(filepath.Join(destination, "app", "Dockerfile"), []byte("FROM alpine:3.21\n"), 0o644))
	require.Error(t, VerifyDockerBuildPlan(destination, plan))
}

func TestValidateBuildRequestOutputDirectory(t *testing.T) {
	// A nil request carries no directory and selects the legacy in-agent build.
	require.NoError(t, ValidateBuildRequestOutputDirectory(nil))

	// Empty selects the legacy in-agent build and is valid.
	require.NoError(t, ValidateBuildRequestOutputDirectory(&builderv0.BuildRequest{}))

	// An absolute destination honors the contract.
	require.NoError(t, ValidateBuildRequestOutputDirectory(&builderv0.BuildRequest{
		OutputDirectory: filepath.Join(t.TempDir(), "recipes"),
	}))

	// A relative destination is rejected at the boundary that populates the field.
	err := ValidateBuildRequestOutputDirectory(&builderv0.BuildRequest{OutputDirectory: "recipes/out"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "absolute")
}

func TestDockerBuildRequestEnforcesAbsoluteOutputDirectory(t *testing.T) {
	wrapper := &BuilderWrapper{Base: &Base{Wool: wool.Get(context.Background())}}
	dockerContext := &builderv0.BuildContext{
		Kind: &builderv0.BuildContext_DockerBuildContext{DockerBuildContext: &builderv0.DockerBuildContext{}},
	}

	// A relative output_directory is rejected before the build proceeds, so the
	// agent never writes recipes where the caller cannot find them.
	_, err := wrapper.DockerBuildRequest(context.Background(), &builderv0.BuildRequest{
		BuildContext:    dockerContext,
		OutputDirectory: "recipes/out",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "absolute")

	// An absolute output_directory passes through to the docker build context.
	got, err := wrapper.DockerBuildRequest(context.Background(), &builderv0.BuildRequest{
		BuildContext:    dockerContext,
		OutputDirectory: filepath.Join(t.TempDir(), "recipes"),
	})
	require.NoError(t, err)
	require.NotNil(t, got)

	// An empty output_directory (legacy in-agent build) passes through.
	got, err = wrapper.DockerBuildRequest(context.Background(), &builderv0.BuildRequest{BuildContext: dockerContext})
	require.NoError(t, err)
	require.NotNil(t, got)
}

func TestBuildDockerBuildPlanDigestChangesWithContent(t *testing.T) {
	destination := t.TempDir()
	dockerfile := filepath.Join(destination, "Dockerfile")
	require.NoError(t, os.WriteFile(dockerfile, []byte("FROM alpine\n"), 0o644))
	first, err := BuildDockerBuildPlan(destination, nil)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(dockerfile, []byte("FROM alpine:3.21\n"), 0o644))
	second, err := BuildDockerBuildPlan(destination, nil)
	require.NoError(t, err)

	require.NotEqual(t, first.GetDigest(), second.GetDigest())
}
