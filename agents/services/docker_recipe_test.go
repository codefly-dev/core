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

// A plan that passes must be buildable: a recipe whose Dockerfile is not in the
// emitted tree is rejected rather than deferred to a confusing buildx failure.
func TestBuildDockerBuildPlanRejectsRecipeReferencingMissingFile(t *testing.T) {
	destination := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(destination, "Dockerfile"), []byte("FROM alpine\n"), 0o644))

	_, err := BuildDockerBuildPlan(destination, []*builderv0.DockerBuildRecipe{
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

	_, err := BuildDockerBuildPlan(destination, []*builderv0.DockerBuildRecipe{
		{Name: "app", Dockerfile: "../Dockerfile", Context: ".", Image: "repo/app:v1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "escapes the output directory")

	_, err = BuildDockerBuildPlan(destination, []*builderv0.DockerBuildRecipe{
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

	_, err := BuildDockerBuildPlan(destination, []*builderv0.DockerBuildRecipe{
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
	recipes := []*builderv0.DockerBuildRecipe{
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
	recipes := []*builderv0.DockerBuildRecipe{
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

	_, err := BuildDockerBuildPlan(destination, []*builderv0.DockerBuildRecipe{
		{Name: "app", Dockerfile: "Dockerfile", Context: "context", Image: "repo/app:v1"},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "symlinks are not permitted")
}

// A plan emitted by an older core carries the previous contract version. It is
// rejected as a contract mismatch — a clear, actionable error — rather than
// slipping into the digest comparison where a version-driven algorithm change
// would surface as an indistinguishable "tamper" failure.
func TestVerifyDockerBuildPlanRejectsStaleContractVersion(t *testing.T) {
	destination := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(destination, "Dockerfile"), []byte("FROM alpine\n"), 0o644))
	recipes := []*builderv0.DockerBuildRecipe{
		{Name: "app", Dockerfile: "Dockerfile", Context: ".", Image: "repo/app:v1"},
	}

	plan, err := BuildDockerBuildPlan(destination, recipes)
	require.NoError(t, err)
	plan.ContractVersion = "codefly.dev/docker-build-recipe/v1"

	err = VerifyDockerBuildPlan(destination, plan)
	require.Error(t, err)
	require.Contains(t, err.Error(), "contract")
	require.NotContains(t, err.Error(), "does not match plan digest")
}
