package services

import (
	"os"
	"path/filepath"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
)

// output_directory is the service's committed builder/ directory, so unrelated
// content lands there routinely: an OS sidecar file, an editor backup, a symlink
// to shared configuration. None of it is a build input — the build context is the
// service directory, not this one — so none of it may perturb the plan or fail
// the build. Scoping the inventory to the emitted set is what guarantees that.
func TestPlanIgnoresContentThisBuildDidNotEmit(t *testing.T) {
	dir, emitted := writeRecipeTree(t, true)
	clean, err := SingleImageBuildPlan(dir, "repo/app:v1", RecipeBuildPlatforms(), emitted)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte("mac"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile~"), []byte("backup"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(filepath.Dir(dir), "shared.conf"), []byte("x"), 0o644))
	require.NoError(t, os.Symlink(filepath.Join(filepath.Dir(dir), "shared.conf"), filepath.Join(dir, "shared.conf")))

	dirty, err := SingleImageBuildPlan(dir, "repo/app:v1", RecipeBuildPlatforms(), emitted)
	require.NoError(t, err, "an unrelated symlink in the committed recipe directory must not fail the build")
	require.Equal(t, clean.GetDigest(), dirty.GetDigest(), "unrelated files must not perturb the recipe digest")

	// Verification must tolerate the same content for the same reason.
	require.NoError(t, VerifyDockerBuildPlan(dir, dirty))
}

// The drift the plan exists to detect must still be caught, on every axis the
// digest covers: content, permission bits, and a declared file replaced by a
// symlink or removed outright.
func TestVerifyStillCatchesDriftInDeclaredFiles(t *testing.T) {
	for name, mutate := range map[string]func(t *testing.T, dir string){
		"content": func(t *testing.T, dir string) {
			require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM evil\n"), 0o644))
		},
		"mode": func(t *testing.T, dir string) {
			require.NoError(t, os.Chmod(filepath.Join(dir, "Dockerfile"), 0o755))
		},
		"symlinked": func(t *testing.T, dir string) {
			outside := filepath.Join(filepath.Dir(dir), "outside")
			require.NoError(t, os.WriteFile(outside, []byte("FROM alpine\nCOPY . .\n"), 0o644))
			require.NoError(t, os.Remove(filepath.Join(dir, "Dockerfile")))
			require.NoError(t, os.Symlink(outside, filepath.Join(dir, "Dockerfile")))
		},
		"removed": func(t *testing.T, dir string) {
			require.NoError(t, os.Remove(filepath.Join(dir, "dockerignore")))
		},
	} {
		t.Run(name, func(t *testing.T) {
			dir, emitted := writeRecipeTree(t, true)
			plan, err := SingleImageBuildPlan(dir, "repo/app:v1", RecipeBuildPlatforms(), emitted)
			require.NoError(t, err)
			require.NoError(t, VerifyDockerBuildPlan(dir, plan))
			mutate(t, dir)
			require.Error(t, VerifyDockerBuildPlan(dir, plan))
		})
	}
}

// A dockerignore this build did not emit must not be adopted into the recipe: the
// executor copies the referenced ignore to the path buildx discovers and applies
// it, so a stale file left by an older template set would silently keep files out
// of the image with no error and no diff.
func TestSingleImageBuildPlanIgnoresStaleDockerignore(t *testing.T) {
	dir, _ := writeRecipeTree(t, true)

	plan, err := SingleImageBuildPlan(dir, "repo/app:v1", RecipeBuildPlatforms(), []string{"Dockerfile"})
	require.NoError(t, err)
	require.Empty(t, plan.GetRecipes()[0].GetDockerignore())
	require.Len(t, plan.GetFiles(), 1)
	require.Equal(t, "Dockerfile", plan.GetFiles()[0].GetPath())
}

// The executor resolves context against the SERVICE directory, not against
// output_directory. Core must not reject a context that is valid there — the old
// os.Stat resolved "code" to <output_directory>/code and refused every service
// whose context was a subdirectory.
func TestRecipeContextIsNotResolvedAgainstTheOutputDirectory(t *testing.T) {
	dir, emitted := writeRecipeTree(t, false)
	recipe := &builderv0.DockerBuildRecipe{
		Name: "app", Dockerfile: "Dockerfile", Context: "code",
		Image: "repo/app:v1", Platforms: RecipeBuildPlatforms(),
	}
	plan, err := BuildEmittedDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe}, emitted)
	require.NoError(t, err)
	require.Equal(t, "code", plan.GetRecipes()[0].GetContext())
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))
}

// Lexical containment still holds: a context or Dockerfile that escapes is
// rejected, so a plan can never point buildx outside the roots it is given.
func TestRecipePathsStillRejectEscapes(t *testing.T) {
	dir, emitted := writeRecipeTree(t, false)
	for name, recipe := range map[string]*builderv0.DockerBuildRecipe{
		"context escapes":    {Name: "app", Dockerfile: "Dockerfile", Context: "../..", Image: "i"},
		"context absolute":   {Name: "app", Dockerfile: "Dockerfile", Context: "/etc", Image: "i"},
		"dockerfile escapes": {Name: "app", Dockerfile: "../Dockerfile", Context: ".", Image: "i"},
		"empty name":         {Dockerfile: "Dockerfile", Context: ".", Image: "i"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := BuildEmittedDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe}, emitted)
			require.Error(t, err)
		})
	}
}

// A plan emitted by an older core carries an older contract version and must be
// reported as a contract mismatch, not silently verified or read as tampering.
func TestVerifyRejectsSupersededContractVersion(t *testing.T) {
	dir, emitted := writeRecipeTree(t, true)
	plan, err := SingleImageBuildPlan(dir, "repo/app:v1", RecipeBuildPlatforms(), emitted)
	require.NoError(t, err)
	require.Equal(t, "codefly.dev/docker-build-recipe/v3", plan.GetContractVersion())
	plan.ContractVersion = "codefly.dev/docker-build-recipe/v2"
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "contract")
}
