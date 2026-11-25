package services

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	builderv1 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v1"
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
	recipes := []*builderv1.DockerBuildRecipe{
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
	// A nil request carries no directory and requires no recipe destination.
	require.NoError(t, ValidateBuildRequestOutputDirectory(nil))

	// Empty requires no recipe destination and is valid.
	require.NoError(t, ValidateBuildRequestOutputDirectory(&builderv1.BuildRequest{}))

	// An absolute destination honors the contract.
	require.NoError(t, ValidateBuildRequestOutputDirectory(&builderv1.BuildRequest{
		OutputDirectory: filepath.Join(t.TempDir(), "recipes"),
	}))

	// A relative destination is rejected at the boundary that populates the field.
	err := ValidateBuildRequestOutputDirectory(&builderv1.BuildRequest{OutputDirectory: "recipes/out"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "absolute")
}

func TestDockerBuildRequestEnforcesAbsoluteOutputDirectory(t *testing.T) {
	wrapper := &BuilderWrapper{Base: &Base{Wool: wool.Get(context.Background())}}
	dockerContext := &builderv1.BuildContext{
		Kind: &builderv1.BuildContext_DockerBuildContext{DockerBuildContext: &builderv1.DockerBuildContext{}},
	}

	// A relative output_directory is rejected before the build proceeds, so the
	// agent never writes recipes where the caller cannot find them.
	_, err := wrapper.DockerBuildRequest(context.Background(), &builderv1.BuildRequest{
		BuildContext:    dockerContext,
		OutputDirectory: "recipes/out",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "absolute")

	// An absolute output_directory passes through to the docker build context.
	got, err := wrapper.DockerBuildRequest(context.Background(), &builderv1.BuildRequest{
		BuildContext:    dockerContext,
		OutputDirectory: filepath.Join(t.TempDir(), "recipes"),
	})
	require.NoError(t, err)
	require.NotNil(t, got)

	// No-build requests may omit output_directory.
	got, err = wrapper.DockerBuildRequest(context.Background(), &builderv1.BuildRequest{BuildContext: dockerContext})
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

// A plan that passes must be buildable: a recipe whose Dockerfile is not in the
// emitted tree is rejected rather than deferred to a confusing buildx failure.
func TestBuildDockerBuildPlanRejectsRecipeReferencingMissingFile(t *testing.T) {
	destination := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(destination, "Dockerfile"), []byte("FROM alpine\n"), 0o644))

	_, err := BuildDockerBuildPlan(destination, []*builderv1.DockerBuildRecipe{
		{Name: "app", Dockerfile: "app/Dockerfile", Context: ".", Image: "repo/app:v1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not present in the recipe tree")
}

// Recipe paths must stay inside the caller-owned output directory: neither a
// traversing relative path nor an absolute path may point buildx elsewhere.
func TestBuildDockerBuildPlanRejectsRecipePathEscapingOutputDirectory(t *testing.T) {
	destination := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(destination, "Dockerfile"), []byte("FROM alpine\n"), 0o644))

	_, err := BuildDockerBuildPlan(destination, []*builderv1.DockerBuildRecipe{
		{Name: "app", Dockerfile: "../Dockerfile", Context: ".", Image: "repo/app:v1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes the output directory")

	_, err = BuildDockerBuildPlan(destination, []*builderv1.DockerBuildRecipe{
		{Name: "app", Dockerfile: "Dockerfile", Context: "/etc", Image: "repo/app:v1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "must be relative")
}

// Recipe names are the logical identity within a service and must be unique;
// two recipes sharing a name is rejected rather than silently building both
// under the same identity.
func TestBuildDockerBuildPlanRejectsDuplicateRecipeName(t *testing.T) {
	destination := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(destination, "one"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(destination, "two"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(destination, "one", "Dockerfile"), []byte("FROM alpine\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(destination, "two", "Dockerfile"), []byte("FROM alpine\n"), 0o644))

	_, err := BuildDockerBuildPlan(destination, []*builderv1.DockerBuildRecipe{
		{Name: "app", Dockerfile: "one/Dockerfile", Context: ".", Image: "repo/app:v1"},
		{Name: "app", Dockerfile: "two/Dockerfile", Context: ".", Image: "repo/app:v2"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not unique")
}

// The aggregate digest covers the recipes, not only the file inventory, so
// tampering with a recipe's image reference while leaving the on-disk files
// byte-identical is caught by verification.
func TestVerifyDockerBuildPlanRejectsTamperedRecipe(t *testing.T) {
	destination := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(destination, "Dockerfile"), []byte("FROM alpine\n"), 0o644))
	recipes := []*builderv1.DockerBuildRecipe{
		{Name: "app", Dockerfile: "Dockerfile", Context: ".", Image: "repo/app:v1"},
	}

	plan, err := BuildDockerBuildPlan(destination, recipes)
	require.NoError(t, err)
	require.NoError(t, VerifyDockerBuildPlan(destination, plan))

	plan.Recipes[0].Image = "attacker/app:v1"
	err = VerifyDockerBuildPlan(destination, plan)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match plan digest")
}

// The aggregate digest covers each file's permission bits, so flipping a recipe
// file's executable bit after the plan is emitted — content byte-identical, a
// change buildx carries into the image — is caught by verification.
func TestVerifyDockerBuildPlanRejectsModeTamper(t *testing.T) {
	destination := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(destination, "Dockerfile"), []byte("FROM alpine\n"), 0o644))
	entrypoint := filepath.Join(destination, "entrypoint.sh")
	require.NoError(t, os.WriteFile(entrypoint, []byte("#!/bin/sh\necho hi\n"), 0o644))
	recipes := []*builderv1.DockerBuildRecipe{
		{Name: "app", Dockerfile: "Dockerfile", Context: ".", Image: "repo/app:v1"},
	}

	plan, err := BuildDockerBuildPlan(destination, recipes)
	require.NoError(t, err)
	require.NoError(t, VerifyDockerBuildPlan(destination, plan))

	// Flip the executable bit without touching content.
	require.NoError(t, os.Chmod(entrypoint, 0o755))
	err = VerifyDockerBuildPlan(destination, plan)
	require.Error(t, err)
	require.Contains(t, err.Error(), "does not match plan digest")
}

// A symlink in the recipe tree is the escape the lexical path check cannot see:
// it is rejected outright rather than followed to an out-of-tree target.
func TestBuildDockerBuildPlanRejectsSymlinkInTree(t *testing.T) {
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "evil"), []byte("FROM scratch\n"), 0o644))

	destination := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(destination, "context"), 0o755))
	// Dockerfile is a symlink pointing at a file outside the output directory.
	if err := os.Symlink(filepath.Join(outside, "evil"), filepath.Join(destination, "Dockerfile")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	_, err := BuildDockerBuildPlan(destination, []*builderv1.DockerBuildRecipe{
		{Name: "app", Dockerfile: "Dockerfile", Context: "context", Image: "repo/app:v1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "symlinks are not permitted")
}

// A plan stating any contract but the one there is carries a label this host
// does not know. It is refused by name — a clear, actionable error — rather
// than slipping into the digest comparison.
func TestVerifyDockerBuildPlanRejectsAnotherContractByName(t *testing.T) {
	destination := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(destination, "Dockerfile"), []byte("FROM alpine\n"), 0o644))
	recipes := []*builderv1.DockerBuildRecipe{
		{Name: "app", Dockerfile: "Dockerfile", Context: ".", Image: "repo/app:v1"},
	}

	plan, err := BuildDockerBuildPlan(destination, recipes)
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeContractVersion, plan.GetContractVersion())
	require.NoError(t, VerifyDockerBuildPlan(destination, plan))

	for _, stated := range []string{
		"codefly.dev/docker-build-recipe/later",
		"codefly.dev/docker-build-recipe/v3", // version-guard:allow a label the contract used to state, refused by name
		"codefly.dev/docker-build-recipe/v6", // version-guard:allow a label the contract used to state, refused by name
		"",
	} {
		plan.ContractVersion = stated
		err = VerifyDockerBuildPlan(destination, plan)
		require.Error(t, err, stated)
		require.Contains(t, err.Error(), "which is unknown", stated)
		require.Contains(t, err.Error(), "states the contract as "+DockerBuildRecipeContractVersion, stated)
		require.NotContains(t, err.Error(), "does not match plan digest", stated)
	}
}

// A plan whose digest was computed the way an earlier recipe format did covers
// less than a plan must. Stating the current contract does not make it one: it
// is refused for what it carries, and the refusal names the cause instead of
// reporting a digest mismatch, which reads as tampering.
func TestVerifyDockerBuildPlanRefusesAnEarlierDigestByContent(t *testing.T) {
	destination := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(destination, "Dockerfile"), []byte("FROM alpine\n"), 0o644))
	recipes := []*builderv1.DockerBuildRecipe{{
		Name: "app", Dockerfile: "Dockerfile", Context: ".", Image: "repo/app:v1",
		ContextRoot:        builderv1.RecipeContextRoot_RECIPE_CONTEXT_ROOT_OUTPUT,
		GoModuleDownloads:  []*builderv1.GoModuleDownload{{ModuleRoot: "code", ProxyContext: "gomodproxy"}},
		GitSourceDownloads: []*builderv1.GitSourceDownload{gitSource()},
	}}
	plan, err := BuildDockerBuildPlan(destination, recipes)
	require.NoError(t, err)
	require.NoError(t, VerifyDockerBuildPlan(destination, plan))
	current := plan.GetDigest()

	earlier := map[string]string{
		"file paths and content":      pathsAndContentRecipeDigest(plan.GetFiles()),
		"not the inventory scope":     recipeDigestCovering(recipeCoversRecipesAndFiles, recipes, plan.GetFiles(), plan.GetScope()),
		"not the context root":        recipeDigestCovering(recipeCoversScope, recipes, plan.GetFiles(), plan.GetScope()),
		"not the Go module downloads": recipeDigestCovering(recipeCoversContextRoot, recipes, plan.GetFiles(), plan.GetScope()),
		"and not the git sources":     recipeDigestCovering(recipeCoversGoModules, recipes, plan.GetFiles(), plan.GetScope()),
	}
	seen := map[string]bool{current: true}
	for names, digest := range earlier {
		require.False(t, seen[digest], "an earlier digest must differ from the current one and from each other: %s", names)
		seen[digest] = true

		plan.Digest = digest
		err := VerifyDockerBuildPlan(destination, plan)
		require.Error(t, err, names)
		require.Contains(t, err.Error(), "an earlier recipe format", names)
		require.Contains(t, err.Error(), names)
		require.Contains(t, err.Error(), "whatever contract the plan states", names)
		require.NotContains(t, err.Error(), "does not match plan digest", names)
	}

	// The same holds for a recipe that declares nothing an earlier format
	// lacked: its earlier digest is still not the current one.
	plain := []*builderv1.DockerBuildRecipe{{Name: "app", Dockerfile: "Dockerfile", Context: ".", Image: "repo/app:v1"}}
	plainPlan, err := BuildDockerBuildPlan(destination, plain)
	require.NoError(t, err)
	plainPlan.Digest = recipeDigestCovering(recipeCoversScope, plain, plainPlan.GetFiles(), plainPlan.GetScope())
	require.ErrorContains(t, VerifyDockerBuildPlan(destination, plainPlan), "an earlier recipe format")

	// A digest that is no format's digest stays what it is: a mismatch.
	plan.Digest = "sha256:" + strings.Repeat("0", 64)
	err = VerifyDockerBuildPlan(destination, plan)
	require.ErrorContains(t, err, "does not match plan digest")
	require.NotContains(t, err.Error(), "an earlier recipe format")

	// The format before plans declared their inventory scope is refused for
	// that, before its contract is read.
	plan.Digest = current
	plan.Scope = builderv1.RecipeInventoryScope_RECIPE_INVENTORY_SCOPE_UNSPECIFIED
	plan.ContractVersion = "codefly.dev/docker-build-recipe/later"
	err = VerifyDockerBuildPlan(destination, plan)
	require.ErrorContains(t, err, "declares no inventory scope")
	require.ErrorContains(t, err, "whatever contract the plan states")
	require.NotContains(t, err.Error(), "which is unknown")
}
