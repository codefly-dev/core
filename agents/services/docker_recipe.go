package services

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// DockerBuildRecipeContractVersion identifies the recipe contract a caller
// validates before building from an emitted plan.
const DockerBuildRecipeContractVersion = "codefly.dev/docker-build-recipe/v1"

// ValidateBuildRequestOutputDirectory enforces the BuildRequest.output_directory
// contract at the boundary that populates it: when set, the destination must be
// an absolute path the caller owns. Empty is valid and selects the legacy
// in-agent build. The CLI resolves its destination to absolute and calls this
// before sending a BuildRequest, so a relative path is rejected with a clear
// error instead of being silently resolved against whatever working directory
// the agent happens to run in. BuildDockerBuildPlan cannot enforce this — its
// tree walk is relative-safe, so a guard there would reject valid callers rather
// than catch the real failure at the point the field is set.
func ValidateBuildRequestOutputDirectory(req *builderv0.BuildRequest) error {
	dir := req.GetOutputDirectory()
	if dir == "" {
		return nil
	}
	if !filepath.IsAbs(dir) {
		return fmt.Errorf("BuildRequest.output_directory must be absolute, got %q", dir)
	}
	return nil
}

// BuildDockerBuildPlan inventories the recipe tree an agent wrote to destination
// and returns a build plan: the ordered recipes plus the canonical sorted file
// inventory with per-file sha256 digests and an aggregate digest that is a
// deterministic function of that inventory. The caller (the CLI) verifies the
// on-disk tree against the plan before running docker buildx, so the recipe is a
// durable, first-class artifact rather than an image built inside the agent.
func BuildDockerBuildPlan(destination string, recipes []*builderv0.DockerBuildRecipe) (*builderv0.DockerBuildPlan, error) {
	files, err := inventoryRecipeFiles(destination)
	if err != nil {
		return nil, fmt.Errorf("inventory recipe tree: %w", err)
	}
	return &builderv0.DockerBuildPlan{
		Recipes:         recipes,
		Files:           files,
		Digest:          aggregateRecipeDigest(files),
		ContractVersion: DockerBuildRecipeContractVersion,
	}, nil
}

// VerifyDockerBuildPlan re-inventories the recipe tree at destination and checks
// it against plan. The caller (the CLI) runs this before docker buildx so it
// never builds from a tree that drifted from the inventory the agent validated.
func VerifyDockerBuildPlan(destination string, plan *builderv0.DockerBuildPlan) error {
	if plan == nil {
		return fmt.Errorf("build plan is nil")
	}
	if plan.GetContractVersion() != DockerBuildRecipeContractVersion {
		return fmt.Errorf("build plan contract %q, expected %q", plan.GetContractVersion(), DockerBuildRecipeContractVersion)
	}
	files, err := inventoryRecipeFiles(destination)
	if err != nil {
		return fmt.Errorf("inventory recipe tree: %w", err)
	}
	if digest := aggregateRecipeDigest(files); digest != plan.GetDigest() {
		return fmt.Errorf("recipe tree digest %s does not match plan digest %s", digest, plan.GetDigest())
	}
	return nil
}

func inventoryRecipeFiles(destination string) ([]*builderv0.RecipeFile, error) {
	var files []*builderv0.RecipeFile
	err := filepath.WalkDir(destination, func(entryPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		content, readErr := os.ReadFile(entryPath)
		if readErr != nil {
			return readErr
		}
		relative, relErr := filepath.Rel(destination, entryPath)
		if relErr != nil {
			return relErr
		}
		sum := sha256.Sum256(content)
		files = append(files, &builderv0.RecipeFile{
			Path:   filepath.ToSlash(relative),
			Digest: "sha256:" + hex.EncodeToString(sum[:]),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].GetPath() < files[j].GetPath() })
	return files, nil
}

func aggregateRecipeDigest(files []*builderv0.RecipeFile) string {
	hasher := sha256.New()
	for _, file := range files {
		fmt.Fprintf(hasher, "%s\x00%s\n", file.GetPath(), file.GetDigest())
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}
