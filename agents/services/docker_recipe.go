package services

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// DockerBuildRecipeContractVersion identifies the recipe contract a caller
// validates before building from an emitted plan. The version is bumped whenever
// the aggregate-digest algorithm changes, so a digest produced by an older core
// is reported as a contract mismatch (a clear, actionable error) rather than as
// a digest mismatch (indistinguishable from tampering). v2 covers the recipes
// and per-file mode in the digest; v1 covered only file paths and content.
const DockerBuildRecipeContractVersion = "codefly.dev/docker-build-recipe/v2"

// ValidateBuildRequestOutputDirectory enforces the BuildRequest.output_directory
// contract: when set, the destination must be an absolute path the caller owns.
// Empty is valid and selects the legacy in-agent build. A relative path is
// rejected with a clear error rather than silently resolved against whatever
// working directory the agent happens to run in — the agent and the caller would
// otherwise resolve it against different directories and the recipe handshake
// would break with no error at all. DockerBuildRequest calls this so every agent
// build enforces the invariant at the boundary where the request enters core;
// the CLI resolves its destination to absolute and can call it before sending.
// BuildDockerBuildPlan does not enforce it — it takes a bare destination and its
// tree walk is relative-safe, so a guard there would reject valid callers of a
// generic helper rather than catch the contract violation at the request boundary.
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
// deterministic function of both the recipes and that inventory. Every recipe is
// validated to reference real, contained tree entries before the plan is
// returned, so a plan that passes is buildable. The caller (the CLI) verifies the
// on-disk tree against the plan before running docker buildx, so the recipe is a
// durable, first-class artifact rather than an image built inside the agent.
func BuildDockerBuildPlan(destination string, recipes []*builderv0.DockerBuildRecipe) (*builderv0.DockerBuildPlan, error) {
	files, err := inventoryRecipeFiles(destination)
	if err != nil {
		return nil, fmt.Errorf("inventory recipe tree: %w", err)
	}
	if err := validateRecipes(destination, recipes, files); err != nil {
		return nil, err
	}
	return &builderv0.DockerBuildPlan{
		Recipes:         recipes,
		Files:           files,
		Digest:          aggregateRecipeDigest(recipes, files),
		ContractVersion: DockerBuildRecipeContractVersion,
	}, nil
}

// RecipeBuildPlatforms is the platform set an emitted recipe targets: a
// linux/amd64 + linux/arm64 manifest list, so a pushed image runs on any
// deployment node regardless of the builder's host architecture. It is
// deliberately fixed and does NOT read the single-platform CODEFLY_BUILD_PLATFORM
// override the legacy in-process build honored: the recipe is a durable,
// reproducible artifact whose digest must not vary with the emitting machine's
// environment, and a pushed deploy image must always carry the deployment
// architecture. Narrowing to a single arch for a faster LOCAL (unpushed) build is
// the caller's concern — the CLI selects a host-matching platform from this list
// for a --load build — not something the recipe encodes.
func RecipeBuildPlatforms() []string {
	return []string{"linux/amd64", "linux/arm64"}
}

// BuildPlanRequested reports whether the caller (the CLI) owns the build for this
// request — a non-empty BuildRequest.output_directory means the runner should emit
// a recipe into that directory instead of building the image in-process. This is
// the single negotiation point every language runner checks, so the recipe path is
// language-agnostic: Go, Rust, Python, Node — any agent built on the shared builder
// switches to CLI-owned builds by consulting this and nothing else.
func BuildPlanRequested(req *builderv0.BuildRequest) bool {
	return req.GetOutputDirectory() != ""
}

// SingleImageBuildPlan assembles the plan for a service that emits one image from
// a Dockerfile the agent rendered into outputDirectory — the service's committed
// builder/ recipe directory, and the value the caller passes as
// BuildRequest.output_directory. The Dockerfile and optional dockerignore live
// directly in outputDirectory (paths are relative to it, not to a nested builder/
// subdirectory); the build context "." is the service directory, which the caller
// (the CLI) resolves and passes to docker buildx. A runner calls it when the
// caller requested recipe emission (a non-empty BuildRequest.output_directory)
// instead of building the image in-process, so the build recipe becomes a durable
// artifact the caller builds.
func SingleImageBuildPlan(outputDirectory, image string, platforms []string) (*builderv0.DockerBuildPlan, error) {
	dockerignore := ""
	// Lstat, not Stat: inventoryRecipeFiles rejects symlinks outright, so a
	// symlinked dockerignore that Stat would follow-and-accept must not enter the
	// recipe — it would hard-fail the inventory with an error unrelated to the
	// dockerignore reference.
	if info, err := os.Lstat(filepath.Join(outputDirectory, "dockerignore")); err == nil && info.Mode().IsRegular() {
		dockerignore = "dockerignore"
	}
	recipe := &builderv0.DockerBuildRecipe{
		Name:         "app",
		Dockerfile:   "Dockerfile",
		Context:      ".",
		Dockerignore: dockerignore,
		Image:        image,
		Platforms:    platforms,
	}
	return BuildDockerBuildPlan(outputDirectory, []*builderv0.DockerBuildRecipe{recipe})
}

// VerifyDockerBuildPlan re-inventories the recipe tree at destination and checks
// it against plan. The caller (the CLI) runs this before docker buildx so it
// never builds from a tree that drifted from the inventory the agent validated,
// and never builds recipes whose metadata (image, args, paths) was tampered with
// after the plan was emitted: the digest covers the recipes as well as the files,
// and every recipe is re-validated to reference real, contained tree entries.
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
	if err := validateRecipes(destination, plan.GetRecipes(), files); err != nil {
		return err
	}
	if digest := aggregateRecipeDigest(plan.GetRecipes(), files); digest != plan.GetDigest() {
		return fmt.Errorf("recipe tree digest %s does not match plan digest %s", digest, plan.GetDigest())
	}
	return nil
}

// validateRecipes checks that every recipe carries a non-empty name that is
// unique within the service, and references paths that are relative, contained
// within destination, and present in the inventoried tree: the Dockerfile and
// (optional) dockerignore must be files in the inventory, and the context must
// be an existing directory. A plan that passes this is buildable by docker
// buildx and cannot point the build context or Dockerfile outside the
// caller-owned output directory.
func validateRecipes(destination string, recipes []*builderv0.DockerBuildRecipe, files []*builderv0.RecipeFile) error {
	inventory := make(map[string]struct{}, len(files))
	for _, file := range files {
		inventory[file.GetPath()] = struct{}{}
	}
	seenNames := make(map[string]struct{}, len(recipes))
	for _, recipe := range recipes {
		name := recipe.GetName()
		if name == "" {
			return fmt.Errorf("recipe has an empty name")
		}
		if _, ok := seenNames[name]; ok {
			return fmt.Errorf("recipe name %q is not unique within the service", name)
		}
		seenNames[name] = struct{}{}
		dockerfile, err := recipeRelPath(destination, recipe.GetDockerfile())
		if err != nil {
			return fmt.Errorf("recipe %q dockerfile: %w", recipe.GetName(), err)
		}
		if _, ok := inventory[dockerfile]; !ok {
			return fmt.Errorf("recipe %q dockerfile %q is not present in the recipe tree", recipe.GetName(), recipe.GetDockerfile())
		}
		context, err := recipeRelPath(destination, recipe.GetContext())
		if err != nil {
			return fmt.Errorf("recipe %q context: %w", recipe.GetName(), err)
		}
		info, statErr := os.Stat(filepath.Join(destination, filepath.FromSlash(context)))
		if statErr != nil {
			return fmt.Errorf("recipe %q context %q: %w", recipe.GetName(), recipe.GetContext(), statErr)
		}
		if !info.IsDir() {
			return fmt.Errorf("recipe %q context %q is not a directory", recipe.GetName(), recipe.GetContext())
		}
		if ignore := recipe.GetDockerignore(); ignore != "" {
			dockerignore, err := recipeRelPath(destination, ignore)
			if err != nil {
				return fmt.Errorf("recipe %q dockerignore: %w", recipe.GetName(), err)
			}
			if _, ok := inventory[dockerignore]; !ok {
				return fmt.Errorf("recipe %q dockerignore %q is not present in the recipe tree", recipe.GetName(), recipe.GetDockerignore())
			}
		}
	}
	return nil
}

// recipeRelPath validates that p is a non-empty relative path that stays inside
// destination, and returns it in the slash-separated form used by the inventory.
// Absolute paths and paths that escape destination (via "..") are rejected so a
// recipe can never point buildx at a Dockerfile or context outside the
// caller-owned output directory.
func recipeRelPath(destination, p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("path is empty")
	}
	if filepath.IsAbs(p) {
		return "", fmt.Errorf("path %q must be relative", p)
	}
	clean := filepath.Clean(filepath.FromSlash(p))
	full := filepath.Join(destination, clean)
	rel, err := filepath.Rel(destination, full)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path %q escapes the output directory", p)
	}
	return filepath.ToSlash(rel), nil
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
		// Reject symlinks outright rather than following them. A symlink is the
		// escape the lexical recipeRelPath check cannot see: fileDigest would
		// otherwise hash the symlink's out-of-tree target, and buildx would
		// follow it out of the caller-owned output directory. Rejecting here
		// contains the tree by construction, and replaces the cryptic
		// "is a directory" error a directory symlink used to produce.
		relative, relErr := filepath.Rel(destination, entryPath)
		if relErr != nil {
			return relErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("recipe tree entry %q is a symlink; symlinks are not permitted in the recipe tree", filepath.ToSlash(relative))
		}
		info, infoErr := entry.Info()
		if infoErr != nil {
			return infoErr
		}
		digest, digestErr := fileDigest(entryPath)
		if digestErr != nil {
			return digestErr
		}
		files = append(files, &builderv0.RecipeFile{
			Path:   filepath.ToSlash(relative),
			Digest: digest,
			Mode:   uint32(info.Mode().Perm()),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].GetPath() < files[j].GetPath() })
	return files, nil
}

// fileDigest streams the file through sha256 rather than buffering it whole, so
// inventorying a large build context does not read every file into memory.
func fileDigest(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil)), nil
}

// aggregateRecipeDigest is a deterministic digest over both the recipes and the
// file inventory. Every string is written length-prefixed (netstring form) so no
// field value — including a path that contains a separator or newline — can be
// confused with a field boundary, and map keys are sorted so map iteration order
// does not perturb the result. Because the recipes are covered, a plan whose
// image reference, build args, target, or paths were altered no longer matches
// its digest even when the on-disk files are byte-identical. Each file's Unix
// permission bits are covered too, so flipping a file's executable bit — which
// buildx carries into the image — is detected even when its content is
// unchanged.
func aggregateRecipeDigest(recipes []*builderv0.DockerBuildRecipe, files []*builderv0.RecipeFile) string {
	hasher := sha256.New()
	hashField(hasher, "recipes")
	hashCount(hasher, len(recipes))
	for _, recipe := range recipes {
		hashField(hasher, recipe.GetName())
		hashField(hasher, recipe.GetDockerfile())
		hashField(hasher, recipe.GetContext())
		hashField(hasher, recipe.GetDockerignore())
		hashField(hasher, recipe.GetImage())
		hashField(hasher, recipe.GetTarget())
		platforms := recipe.GetPlatforms()
		hashCount(hasher, len(platforms))
		for _, platform := range platforms {
			hashField(hasher, platform)
		}
		args := recipe.GetBuildArgs()
		keys := make([]string, 0, len(args))
		for key := range args {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		hashCount(hasher, len(keys))
		for _, key := range keys {
			hashField(hasher, key)
			hashField(hasher, args[key])
		}
	}
	hashField(hasher, "files")
	hashCount(hasher, len(files))
	for _, file := range files {
		hashField(hasher, file.GetPath())
		hashField(hasher, file.GetDigest())
		hashCount(hasher, int(file.GetMode()))
	}
	return "sha256:" + hex.EncodeToString(hasher.Sum(nil))
}

func hashField(hasher io.Writer, value string) {
	fmt.Fprintf(hasher, "%d:", len(value))
	io.WriteString(hasher, value)
}

func hashCount(hasher io.Writer, n int) {
	fmt.Fprintf(hasher, "#%d:", n)
}
