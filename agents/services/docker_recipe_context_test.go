package services

import (
	"os"
	"path/filepath"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
)

func TestRecipeContextRootVersionAndIntegrity(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))
	recipe := &builderv0.DockerBuildRecipe{Name: "app", Dockerfile: "Dockerfile", Context: ".", Image: "example/app:v1"}
	unspecified, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeContractVersion, unspecified.GetContractVersion())
	require.NoError(t, VerifyDockerBuildPlan(dir, unspecified))

	recipe.ContextRoot = builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_OUTPUT
	plan, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeContractVersion, plan.GetContractVersion())
	require.NotEqual(t, unspecified.GetDigest(), plan.GetDigest())
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))

	// There is one contract: a plan stating another is refused by name.
	plan.ContractVersion = "codefly.dev/docker-build-recipe/other"
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "contract")
	plan.ContractVersion = DockerBuildRecipeContractVersion

	// The root is always digested, so changing the base is detected.
	recipe.ContextRoot = builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_SERVICE
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "does not match plan digest")

	// Clearing the root cannot downgrade the claim: an unspecified root is
	// digested as one, so the plan no longer matches.
	recipe.ContextRoot = builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_UNSPECIFIED
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "does not match plan digest")
}

func TestRecipeContextRootIsNotInventoryScope(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))
	recipe := &builderv0.DockerBuildRecipe{Name: "app", Dockerfile: "Dockerfile", Context: ".", Image: "example/app:v1", ContextRoot: builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_OUTPUT}
	// A context root is an independent declaration, not inferred from how the
	// agent inventories its output. Both legitimate inventory modes preserve it.
	plan, err := BuildEmittedDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe}, []string{"Dockerfile"})
	require.NoError(t, err)
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))
	require.Equal(t, DockerBuildRecipeContractVersion, plan.GetContractVersion())

	recipe.ContextRoot = builderv0.RecipeContextRoot(99)
	_, err = BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.ErrorContains(t, err, "unknown context root")
}
