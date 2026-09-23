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
	legacy, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.NoError(t, err)
	require.Equal(t, "codefly.dev/docker-build-recipe/v3", legacy.GetContractVersion())
	require.NoError(t, VerifyDockerBuildPlan(dir, legacy))

	recipe.ContextRoot = builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_OUTPUT
	plan, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeContextContractVersion, plan.GetContractVersion())
	require.NotEqual(t, legacy.GetDigest(), plan.GetDigest())
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))

	// Downgrading the version alone must not allow an old host to build source.
	plan.ContractVersion = DockerBuildRecipeContractVersion
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "contract")
	plan.ContractVersion = DockerBuildRecipeContextContractVersion

	// Both explicit roots use v4, so changing the base is detected by the digest.
	recipe.ContextRoot = builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_SERVICE
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "digest")

	// Clearing the root AND changing the version cannot downgrade the claim.
	recipe.ContextRoot = builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_UNSPECIFIED
	plan.ContractVersion = DockerBuildRecipeContractVersion
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "digest")
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
	require.Equal(t, DockerBuildRecipeContextContractVersion, plan.GetContractVersion())

	recipe.ContextRoot = builderv0.RecipeContextRoot(99)
	_, err = BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.ErrorContains(t, err, "unknown context root")
}
