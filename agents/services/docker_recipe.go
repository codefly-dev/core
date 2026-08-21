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
