package services

import (
	"os"
	"path/filepath"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
)

// wholeTreePlan mirrors what a service agent emits when it copies its build
// context into the destination and builds "." — service-go's buildRecipe does
// exactly this, so every file under destination is a real build input.
func wholeTreePlan(t *testing.T) (string, *builderv0.DockerBuildPlan) {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "code"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\nCOPY . .\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "code", "main.go"), []byte("package main\n"), 0o644))
	plan, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{{
		Name: "app", Dockerfile: "Dockerfile", Context: ".", Image: "repo/app:v1",
		Platforms: RecipeBuildPlatforms(),
	}})
	require.NoError(t, err)
	require.Equal(t, builderv0.RecipeInventoryScope_RECIPE_INVENTORY_SCOPE_TREE, plan.GetScope())
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))
	return dir, plan
}

// A whole-tree emitter claims the entire destination, so a file added after
// emission is an injected build input that buildx would copy into the image.
// Verifying such a plan by re-hashing only its declared paths would not see it.
func TestVerifyRejectsFilesAddedToAWholeTreeBuildContext(t *testing.T) {
	dir, plan := wholeTreePlan(t)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "code", "injected.go"), []byte("package main\n"), 0o644))
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "digest")
}

func TestVerifyRejectsSymlinksAddedToAWholeTreeBuildContext(t *testing.T) {
	dir, plan := wholeTreePlan(t)

	outside := filepath.Join(t.TempDir(), "secret")
	require.NoError(t, os.WriteFile(outside, []byte("secret"), 0o644))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "code", "secret")))
	require.Error(t, VerifyDockerBuildPlan(dir, plan))
}

// The emitted-scope claim is the opposite case and must stay tolerant: the
// destination is the service's committed builder/ directory, shared with the
// repository, so content the build did not write is outside the claim.
func TestVerifyToleratesUnclaimedContentInAnEmittedScopePlan(t *testing.T) {
	dir, emitted := writeRecipeTree(t, true)
	plan, err := SingleImageBuildPlan(dir, "repo/app:v1", RecipeBuildPlatforms(), emitted)
	require.NoError(t, err)
	require.Equal(t, builderv0.RecipeInventoryScope_RECIPE_INVENTORY_SCOPE_EMITTED, plan.GetScope())

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("mac"), 0o644))
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))
}

// Downgrading the scope would silently turn the strict check into the tolerant
// one, so the scope is covered by the aggregate digest.
func TestScopeCannotBeDowngradedWithoutBreakingTheDigest(t *testing.T) {
	dir, plan := wholeTreePlan(t)

	plan.Scope = builderv0.RecipeInventoryScope_RECIPE_INVENTORY_SCOPE_EMITTED
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "digest")
}

// Identical recipes and files under different scopes are different claims, so
// they must not share a digest.
func TestScopeParticipatesInTheAggregateDigest(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM alpine\n"), 0o644))
	recipes := []*builderv0.DockerBuildRecipe{{Name: "app", Dockerfile: "Dockerfile", Context: ".", Image: "repo/app:v1"}}

	tree, err := BuildDockerBuildPlan(dir, recipes)
	require.NoError(t, err)
	emitted, err := BuildEmittedDockerBuildPlan(dir, recipes, []string{"Dockerfile"})
	require.NoError(t, err)

	require.Equal(t, tree.GetFiles()[0].GetDigest(), emitted.GetFiles()[0].GetDigest())
	require.NotEqual(t, tree.GetDigest(), emitted.GetDigest())
}

// A plan carrying no scope predates the declaration and cannot be verified under
// either reading; defaulting would pick one silently.
func TestVerifyRejectsAPlanWithNoDeclaredScope(t *testing.T) {
	dir, plan := wholeTreePlan(t)

	plan.Scope = builderv0.RecipeInventoryScope_RECIPE_INVENTORY_SCOPE_UNSPECIFIED
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "no inventory scope")
}
