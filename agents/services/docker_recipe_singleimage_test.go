package services

import (
	"os"
	"path/filepath"
	"testing"

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
