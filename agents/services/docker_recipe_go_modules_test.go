package services

import (
	"os"
	"path/filepath"
	"testing"

	builderv1 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v1"
	"github.com/stretchr/testify/require"
)

func goModuleRecipe() *builderv1.DockerBuildRecipe {
	return &builderv1.DockerBuildRecipe{
		Name: "app", Dockerfile: "Dockerfile", Context: ".", Image: "example/app:v1",
		GoModuleDownloads: []*builderv1.GoModuleDownload{{ModuleRoot: "code", ProxyContext: "gomodproxy"}},
	}
}

func TestRecipeGoModuleDownloadsVersionAndIntegrity(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))

	recipe := goModuleRecipe()
	plan, err := BuildDockerBuildPlan(dir, []*builderv1.DockerBuildRecipe{recipe})
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeContractVersion, plan.GetContractVersion())
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))

	// There is one contract: a plan stating another is refused by name.
	plan.ContractVersion = "codefly.dev/docker-build-recipe/other"
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "contract")
	plan.ContractVersion = DockerBuildRecipeContractVersion

	// The declaration is covered by the digest: moving the module root or
	// renaming the proxy context is detected.
	recipe.GoModuleDownloads[0].ModuleRoot = "other"
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "does not match plan digest")
	recipe.GoModuleDownloads[0].ModuleRoot = "code"
	recipe.GoModuleDownloads[0].ProxyContext = "elsewhere"
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "does not match plan digest")
	recipe.GoModuleDownloads[0].ProxyContext = "gomodproxy"
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))

	// Dropping the declaration cannot downgrade the claim: the downloads are
	// always digested, so a plan that declared one no longer matches.
	recipe.GoModuleDownloads = nil
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "does not match plan digest")
}

func TestRecipeGoModuleDownloadsKeepAnExplicitContextRoot(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))
	recipe := goModuleRecipe()
	recipe.ContextRoot = builderv1.RecipeContextRoot_RECIPE_CONTEXT_ROOT_OUTPUT
	plan, err := BuildDockerBuildPlan(dir, []*builderv1.DockerBuildRecipe{recipe})
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeContractVersion, plan.GetContractVersion())

	// The context root is digested beside the downloads.
	recipe.ContextRoot = builderv1.RecipeContextRoot_RECIPE_CONTEXT_ROOT_SERVICE
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "does not match plan digest")
}

func TestRecipeGoModuleDownloadsAreValidated(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))
	for name, download := range map[string]*builderv1.GoModuleDownload{
		"empty root":          {ModuleRoot: "", ProxyContext: "gomodproxy"},
		"absolute root":       {ModuleRoot: "/code", ProxyContext: "gomodproxy"},
		"escaping root":       {ModuleRoot: "../code", ProxyContext: "gomodproxy"},
		"empty context":       {ModuleRoot: "code", ProxyContext: ""},
		"uppercase context":   {ModuleRoot: "code", ProxyContext: "GoModProxy"},
		"context with a path": {ModuleRoot: "code", ProxyContext: "go/mod"},
	} {
		t.Run(name, func(t *testing.T) {
			recipe := goModuleRecipe()
			recipe.GoModuleDownloads = []*builderv1.GoModuleDownload{download}
			_, err := BuildDockerBuildPlan(dir, []*builderv1.DockerBuildRecipe{recipe})
			require.Error(t, err)
		})
	}

	recipe := goModuleRecipe()
	recipe.GoModuleDownloads = append(recipe.GoModuleDownloads, &builderv1.GoModuleDownload{ModuleRoot: "tools", ProxyContext: "gomodproxy"})
	_, err := BuildDockerBuildPlan(dir, []*builderv1.DockerBuildRecipe{recipe})
	require.ErrorContains(t, err, "twice")
}

func TestSingleImageBuildPlanDeclaresGoModuleDownloads(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))
	plan, err := SingleImageBuildPlan(dir, "example/app:v1", RecipeBuildPlatforms(), []string{"Dockerfile"},
		WithGoModuleDownloads(&builderv1.GoModuleDownload{ModuleRoot: "code", ProxyContext: "gomodproxy"}))
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeContractVersion, plan.GetContractVersion())
	require.Equal(t, "code", plan.GetRecipes()[0].GetGoModuleDownloads()[0].GetModuleRoot())
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))

	// Without the option the helper declares no download, under the same
	// contract and with another digest.
	without, err := SingleImageBuildPlan(dir, "example/app:v1", RecipeBuildPlatforms(), []string{"Dockerfile"})
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeContractVersion, without.GetContractVersion())
	require.Empty(t, without.GetRecipes()[0].GetGoModuleDownloads())
	require.NotEqual(t, plan.GetDigest(), without.GetDigest())
}
