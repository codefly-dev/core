package services

import (
	"os"
	"path/filepath"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
)

func goModuleRecipe() *builderv0.DockerBuildRecipe {
	return &builderv0.DockerBuildRecipe{
		Name: "app", Dockerfile: "Dockerfile", Context: ".", Image: "example/app:v1",
		GoModuleDownloads: []*builderv0.GoModuleDownload{{ModuleRoot: "code", ProxyContext: "gomodproxy"}},
	}
}

func TestRecipeGoModuleDownloadsVersionAndIntegrity(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))

	recipe := goModuleRecipe()
	plan, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeGoModulesContractVersion, plan.GetContractVersion())
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))

	// A host that predates the field would expect v3 for these recipes: the
	// declared version, not the field it cannot see, is what refuses the plan.
	plan.ContractVersion = DockerBuildRecipeContractVersion
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "contract")
	plan.ContractVersion = DockerBuildRecipeGoModulesContractVersion

	// The declaration is covered by the digest: moving the module root or
	// renaming the proxy context is detected.
	recipe.GoModuleDownloads[0].ModuleRoot = "other"
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "digest")
	recipe.GoModuleDownloads[0].ModuleRoot = "code"
	recipe.GoModuleDownloads[0].ProxyContext = "elsewhere"
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "digest")
	recipe.GoModuleDownloads[0].ProxyContext = "gomodproxy"
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))

	// Dropping the declaration AND the version cannot downgrade the claim.
	recipe.GoModuleDownloads = nil
	plan.ContractVersion = DockerBuildRecipeContractVersion
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "digest")
}

func TestRecipeGoModuleDownloadsKeepAnExplicitContextRoot(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))
	recipe := goModuleRecipe()
	recipe.ContextRoot = builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_OUTPUT
	plan, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeGoModulesContractVersion, plan.GetContractVersion())

	// v5 still digests the context root v4 introduced.
	recipe.ContextRoot = builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_SERVICE
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "digest")
}

func TestRecipeGoModuleDownloadsAreValidated(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))
	for name, download := range map[string]*builderv0.GoModuleDownload{
		"empty root":          {ModuleRoot: "", ProxyContext: "gomodproxy"},
		"absolute root":       {ModuleRoot: "/code", ProxyContext: "gomodproxy"},
		"escaping root":       {ModuleRoot: "../code", ProxyContext: "gomodproxy"},
		"empty context":       {ModuleRoot: "code", ProxyContext: ""},
		"uppercase context":   {ModuleRoot: "code", ProxyContext: "GoModProxy"},
		"context with a path": {ModuleRoot: "code", ProxyContext: "go/mod"},
	} {
		t.Run(name, func(t *testing.T) {
			recipe := goModuleRecipe()
			recipe.GoModuleDownloads = []*builderv0.GoModuleDownload{download}
			_, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
			require.Error(t, err)
		})
	}

	recipe := goModuleRecipe()
	recipe.GoModuleDownloads = append(recipe.GoModuleDownloads, &builderv0.GoModuleDownload{ModuleRoot: "tools", ProxyContext: "gomodproxy"})
	_, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.ErrorContains(t, err, "twice")
}

func TestSingleImageBuildPlanDeclaresGoModuleDownloads(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))
	plan, err := SingleImageBuildPlan(dir, "example/app:v1", RecipeBuildPlatforms(), []string{"Dockerfile"},
		WithGoModuleDownloads(&builderv0.GoModuleDownload{ModuleRoot: "code", ProxyContext: "gomodproxy"}))
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeGoModulesContractVersion, plan.GetContractVersion())
	require.Equal(t, "code", plan.GetRecipes()[0].GetGoModuleDownloads()[0].GetModuleRoot())
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))

	// Without the option the helper emits exactly what it always did.
	legacy, err := SingleImageBuildPlan(dir, "example/app:v1", RecipeBuildPlatforms(), []string{"Dockerfile"})
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeContractVersion, legacy.GetContractVersion())
}
