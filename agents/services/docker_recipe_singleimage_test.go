package services

import (
	"os"
	"path/filepath"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
)

func writeRecipeTree(t *testing.T, withIgnore bool) string {
	t.Helper()
	dir := t.TempDir()
	builder := filepath.Join(dir, "builder")
	require.NoError(t, os.MkdirAll(builder, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(builder, "Dockerfile"), []byte("FROM alpine\nCOPY . .\n"), 0o644))
	if withIgnore {
		require.NoError(t, os.WriteFile(filepath.Join(builder, "dockerignore"), []byte("code/node_modules\n"), 0o644))
	}
	return dir
}

func TestSingleImageBuildPlanConventionalLayout(t *testing.T) {
	dir := writeRecipeTree(t, true)

	plan, err := SingleImageBuildPlan(dir, "repo/app:v1", RecipeBuildPlatforms())
	require.NoError(t, err)
	require.Len(t, plan.GetRecipes(), 1)

	recipe := plan.GetRecipes()[0]
	require.Equal(t, "app", recipe.GetName())
	require.Equal(t, "builder/Dockerfile", recipe.GetDockerfile())
	require.Equal(t, ".", recipe.GetContext())
	require.Equal(t, "builder/dockerignore", recipe.GetDockerignore())
	require.Equal(t, "repo/app:v1", recipe.GetImage())
	require.Equal(t, []string{"linux/amd64", "linux/arm64"}, recipe.GetPlatforms())

	// The plan the agent emits must verify against the tree the caller sees.
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))
}

func TestSingleImageBuildPlanOmitsAbsentDockerignore(t *testing.T) {
	dir := writeRecipeTree(t, false)

	plan, err := SingleImageBuildPlan(dir, "repo/app:v1", RecipeBuildPlatforms())
	require.NoError(t, err)
	require.Equal(t, "", plan.GetRecipes()[0].GetDockerignore())
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))
}

func TestRecipeBuildPlatformsCoversDeploymentArch(t *testing.T) {
	require.Contains(t, RecipeBuildPlatforms(), "linux/amd64")
}

func TestBuildPlanRequested(t *testing.T) {
	require.False(t, BuildPlanRequested(&builderv0.BuildRequest{}))
	require.False(t, BuildPlanRequested(&builderv0.BuildRequest{OutputDirectory: ""}))
	require.True(t, BuildPlanRequested(&builderv0.BuildRequest{OutputDirectory: "/abs/out"}))
}

// The shared wrapper path is what makes the recipe emission language-agnostic: any
// language runner returns SingleImageBuildResponse and gets the same DockerBuildPlan
// result, verifiable against the caller's tree.
func TestSingleImageBuildResponseEmitsPlan(t *testing.T) {
	dir := writeRecipeTree(t, true)
	base := &Base{loaded: true}
	wrapper := &BuilderWrapper{Base: base}
	base.Builder = wrapper // WithBuildPlan records onto s.Builder, as production wires it.

	resp, err := wrapper.SingleImageBuildResponse(&builderv0.BuildRequest{OutputDirectory: dir}, "repo/app:v1")
	require.NoError(t, err)
	require.Equal(t, builderv0.BuildStatus_SUCCESS, resp.GetState().GetState())

	plan := resp.GetResult().GetDockerBuildPlan()
	require.NotNil(t, plan)
	require.Len(t, plan.GetRecipes(), 1)
	require.Equal(t, "repo/app:v1", plan.GetRecipes()[0].GetImage())
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))
}
