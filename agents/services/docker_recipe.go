package services

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/codefly-dev/core/templates"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// DockerBuildRecipeContractVersion identifies the recipe contract a caller
// validates before building from an emitted plan. The version is bumped whenever
// the aggregate-digest algorithm changes, so a digest produced by an older core
// is reported as a contract mismatch (a clear, actionable error) rather than as
// a digest mismatch (indistinguishable from tampering). v2 covers the recipes
// and per-file mode in the digest; v1 covered only file paths and content. v3
// makes each plan declare what its inventory covers (RecipeInventoryScope) and
// digests that declaration, so a plan rendered into a directory shared with the
// repository is not failed by unrelated content while a plan covering a whole
// assembled build context still rejects files added to it.
const DockerBuildRecipeContractVersion = "codefly.dev/docker-build-recipe/v3"

// DockerBuildRecipeContextContractVersion adds an explicit context root to the
// recipe digest. Unchanged recipes still emit v3 so existing agents need no
// coordinated rebuild; old hosts reject v4 before executing its new semantics.
const DockerBuildRecipeContextContractVersion = "codefly.dev/docker-build-recipe/v4"

func recipeContractVersion(recipes []*builderv0.DockerBuildRecipe) string {
	for _, recipe := range recipes {
		if recipe.GetContextRoot() != builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_UNSPECIFIED {
			return DockerBuildRecipeContextContractVersion
		}
	}
	return DockerBuildRecipeContractVersion
}

// ValidateBuildRequestOutputDirectory enforces the BuildRequest.output_directory
// contract: when set, the destination must be an absolute path the caller owns.
// Empty is valid for requests that do not build an image. A relative path is
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

// BuilderTemplateRoot is the subtree of an agent's embedded template filesystem
// that WithBuilder renders. PrepareRecipeDestination and WithBuilder must agree
// on it, or the prepared path set would not match the rendered one.
const BuilderTemplateRoot = "templates/builder"

// PrepareRecipeDestination unlinks every path the builder template set is about
// to render into outputDirectory and returns that path set, destination-relative
// and sorted.
//
// It must run before the templates are rendered. The template writer opens each
// destination with os.OpenFile(O_WRONLY|O_CREATE|O_TRUNC) and tests existence
// with os.Stat, and both follow symlinks: rendering over a symlinked destination
// truncates the symlink's target, which can sit anywhere on disk. Because
// output_directory is the service's committed builder/ directory, a symlink
// there is ordinary repository content, not an attack precondition. Unlinking
// first (os.Remove never follows the final component) makes every render land
// on a fresh regular file inside the caller-owned directory.
//
// Inventorying the returned set rather than walking the destination is what
// keeps a plan's digest a function of the recipe: unrelated files in a committed
// directory — editor backups, .DS_Store, a symlink to shared config — are
// neither hashed nor rejected, because none of them is a file this build wrote.
func PrepareRecipeDestination(builderFS fs.FS, outputDirectory string) ([]string, error) {
	var emitted []string
	namer := templates.CutTemplateSuffix{}
	err := fs.WalkDir(builderFS, BuilderTemplateRoot, func(entry string, info fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		// fs.WalkDir always yields slash-separated paths, so the template-relative
		// name is derived with path, not filepath, and converted once on the way to
		// the destination below.
		relative, found := strings.CutPrefix(entry, BuilderTemplateRoot+"/")
		if !found {
			return fmt.Errorf("template entry %q is outside %q", entry, BuilderTemplateRoot)
		}
		name := namer.NewName(path.Clean(relative))
		emitted = append(emitted, name)
		if removeErr := os.Remove(filepath.Join(outputDirectory, filepath.FromSlash(name))); removeErr != nil && !os.IsNotExist(removeErr) {
			return fmt.Errorf("cannot replace recipe file %q: %w", name, removeErr)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("prepare recipe destination: %w", err)
	}
	if len(emitted) == 0 {
		return nil, fmt.Errorf("builder template set %q is empty; nothing to emit", BuilderTemplateRoot)
	}
	sort.Strings(emitted)
	return emitted, nil
}

// BuildEmittedDockerBuildPlan builds a plan whose inventory is exactly the files
// this build emitted — the set PrepareRecipeDestination returned — instead of
// whatever the destination directory currently holds. Use it from any runner
// that renders a known template set; BuildDockerBuildPlan remains for callers
// that assemble a destination tree by other means and want all of it covered.
func BuildEmittedDockerBuildPlan(destination string, recipes []*builderv0.DockerBuildRecipe, emitted []string) (*builderv0.DockerBuildPlan, error) {
	files, err := inventoryRecipePaths(destination, emitted)
	if err != nil {
		return nil, fmt.Errorf("inventory recipe files: %w", err)
	}
	if err := validateRecipes(destination, recipes, files); err != nil {
		return nil, err
	}
	return &builderv0.DockerBuildPlan{
		Recipes:         recipes,
		Files:           files,
		Digest:          aggregateRecipeDigest(recipes, files, builderv0.RecipeInventoryScope_RECIPE_INVENTORY_SCOPE_EMITTED),
		ContractVersion: recipeContractVersion(recipes),
		Scope:           builderv0.RecipeInventoryScope_RECIPE_INVENTORY_SCOPE_EMITTED,
	}, nil
}

// BuildDockerBuildPlan inventories the recipe tree an agent wrote to destination
// and returns a build plan: the ordered recipes plus the canonical sorted file
// inventory with per-file sha256 digests and an aggregate digest that is a
// deterministic function of both the recipes and that inventory. Every recipe is
// validated to reference real, contained tree entries before the plan is
// returned, so a plan that passes is buildable. The caller (the CLI) verifies the
// on-disk tree against the plan before running docker buildx, so the recipe is a
// durable, first-class artifact rather than an image built inside the agent.
//
// This inventories everything under destination, including entries the caller did
// not write, and rejects any symlink it finds. That is the right contract for a
// caller that assembles the whole destination itself and is claiming all of it —
// there, an unexpected entry is drift. A caller that renders a known template set
// into a directory it shares with the repository should use
// BuildEmittedDockerBuildPlan instead, which claims only the files it wrote.
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
		Digest:          aggregateRecipeDigest(recipes, files, builderv0.RecipeInventoryScope_RECIPE_INVENTORY_SCOPE_TREE),
		ContractVersion: recipeContractVersion(recipes),
		Scope:           builderv0.RecipeInventoryScope_RECIPE_INVENTORY_SCOPE_TREE,
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

// BuildPlanRequested reports whether a request supplies a recipe destination.
// Image-building runners must reject requests without one before preparation.
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
//
// emitted is the path set this build rendered, from PrepareRecipeDestination. The
// recipe references a dockerignore only when that set contains one. Probing the
// destination for a dockerignore instead would adopt a stale file: the executor
// copies the referenced ignore to the path buildx discovers and applies it, so an
// ignore left behind by an older template set would silently keep files out of
// the image with no error and no diff.
func SingleImageBuildPlan(outputDirectory, image string, platforms []string, emitted []string) (*builderv0.DockerBuildPlan, error) {
	dockerignore := ""
	for _, name := range emitted {
		if name == "dockerignore" {
			dockerignore = "dockerignore"
			break
		}
	}
	recipe := &builderv0.DockerBuildRecipe{
		Name:         "app",
		Dockerfile:   "Dockerfile",
		Context:      ".",
		Dockerignore: dockerignore,
		Image:        image,
		Platforms:    platforms,
	}
	return BuildEmittedDockerBuildPlan(outputDirectory, []*builderv0.DockerBuildRecipe{recipe}, emitted)
}

// inventoryRecipePaths inventories exactly the destination-relative paths given,
// in canonical sorted order. Each path must be relative, stay inside destination,
// and resolve to a regular file. Symlinks are rejected by Lstat rather than
// followed: fileDigest would otherwise hash an out-of-tree target and buildx
// would read it, so containment is enforced on the real file type, not only on
// the lexical path.
func inventoryRecipePaths(destination string, paths []string) ([]*builderv0.RecipeFile, error) {
	files := make([]*builderv0.RecipeFile, 0, len(paths))
	seen := make(map[string]struct{}, len(paths))
	for _, raw := range paths {
		relative, err := recipeRelPath(destination, raw)
		if err != nil {
			return nil, fmt.Errorf("recipe file %q: %w", raw, err)
		}
		if _, duplicate := seen[relative]; duplicate {
			return nil, fmt.Errorf("recipe file %q is listed more than once", relative)
		}
		seen[relative] = struct{}{}
		full := filepath.Join(destination, filepath.FromSlash(relative))
		info, err := os.Lstat(full)
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("recipe file %q is a symlink; symlinks are not permitted in the recipe tree", relative)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("recipe file %q is not a regular file", relative)
		}
		digest, err := fileDigest(full)
		if err != nil {
			return nil, err
		}
		files = append(files, &builderv0.RecipeFile{
			Path:   relative,
			Digest: digest,
			Mode:   uint32(info.Mode().Perm()),
		})
	}
	sort.Slice(files, func(i, j int) bool { return files[i].GetPath() < files[j].GetPath() })
	return files, nil
}

// VerifyDockerBuildPlan re-inventories a plan's files and checks them against
// plan. The caller (the CLI) runs this before docker buildx so it never builds
// from a tree that drifted from the inventory the agent validated, and never
// builds recipes whose metadata (image, args, paths) was tampered with after the
// plan was emitted: the digest covers the recipes as well as the files, and every
// recipe is re-validated to reference real, contained tree entries.
//
// How much of destination counts as "the plan's files" is the plan's own
// declaration, not a guess here, because the two possible answers are not
// interchangeable:
//
//   - TREE: re-walk destination and compare the whole inventory. The emitter
//     assembled the entire destination — service agents copy the build context
//     there and build "." — so a file ADDED after emission is a build input that
//     would be baked into the image, and must fail verification.
//   - EMITTED: re-hash exactly the declared paths. The emitter rendered a known
//     template set into the service's committed builder/ directory, which it
//     shares with the repository, so unrelated content there is outside the claim
//     and must neither move the digest nor abort the build.
//
// Verifying a TREE plan as if it were EMITTED silently stops detecting injected
// build-context files; verifying an EMITTED plan as if it were TREE fails builds
// over an editor backup. A plan that declares no scope is rejected rather than
// verified under a default, and the scope is covered by the digest so it cannot
// be rewritten to weaken the check.
func VerifyDockerBuildPlan(destination string, plan *builderv0.DockerBuildPlan) error {
	if plan == nil {
		return fmt.Errorf("build plan is nil")
	}
	if expected := recipeContractVersion(plan.GetRecipes()); plan.GetContractVersion() != expected {
		return fmt.Errorf("build plan contract %q, expected %q", plan.GetContractVersion(), expected)
	}

	var files []*builderv0.RecipeFile
	var err error
	switch scope := plan.GetScope(); scope {
	case builderv0.RecipeInventoryScope_RECIPE_INVENTORY_SCOPE_TREE:
		files, err = inventoryRecipeFiles(destination)
	case builderv0.RecipeInventoryScope_RECIPE_INVENTORY_SCOPE_EMITTED:
		declared := make([]string, 0, len(plan.GetFiles()))
		for _, file := range plan.GetFiles() {
			declared = append(declared, file.GetPath())
		}
		files, err = inventoryRecipePaths(destination, declared)
	default:
		return fmt.Errorf("build plan declares no inventory scope (%s); it cannot be verified", scope)
	}
	if err != nil {
		return fmt.Errorf("inventory recipe files: %w", err)
	}

	if err := validateRecipes(destination, plan.GetRecipes(), files); err != nil {
		return err
	}
	if digest := aggregateRecipeDigest(plan.GetRecipes(), files, plan.GetScope()); digest != plan.GetDigest() {
		return fmt.Errorf("recipe tree digest %s does not match plan digest %s", digest, plan.GetDigest())
	}
	return nil
}

// validateRecipes checks that every recipe carries a non-empty name that is
// unique within the service, and references paths that are relative and stay
// inside the root they are resolved against: the Dockerfile and (optional)
// dockerignore must be files in the inventory, so they cannot point buildx -f at
// anything outside the caller-owned output directory.
//
// The context is checked lexically only. dockerfile and dockerignore are
// output_directory-relative, but context is resolved by the executor against the
// SERVICE directory — that is what makes "." mean "build the service" while the
// Dockerfile lives in the service's builder/ subdirectory. destination is
// therefore the wrong root to resolve the context against, and statting it here
// proved nothing: it accepted "." because output_directory trivially exists, and
// it rejected a perfectly valid context such as "code" whenever the service had
// no builder/code directory. Existence and containment of the context belong to
// the executor, which is the only party that knows the service directory.
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
		switch recipe.GetContextRoot() {
		case builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_UNSPECIFIED,
			builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_SERVICE,
			builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_OUTPUT:
		default:
			return fmt.Errorf("recipe %q has unknown context root %d", recipe.GetName(), recipe.GetContextRoot())
		}
		if _, err := recipeRelPath(destination, recipe.GetContext()); err != nil {
			return fmt.Errorf("recipe %q context: %w", recipe.GetName(), err)
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
func aggregateRecipeDigest(recipes []*builderv0.DockerBuildRecipe, files []*builderv0.RecipeFile, scope builderv0.RecipeInventoryScope) string {
	hasher := sha256.New()
	explicitRoots := recipeContractVersion(recipes) == DockerBuildRecipeContextContractVersion
	// The scope decides how strictly the inventory is verified, so it is covered
	// here: rewriting a TREE plan's scope to EMITTED would otherwise silently stop
	// added files from being detected without disturbing the digest.
	hashField(hasher, "scope")
	hashField(hasher, scope.String())
	hashField(hasher, "recipes")
	hashCount(hasher, len(recipes))
	for _, recipe := range recipes {
		hashField(hasher, recipe.GetName())
		hashField(hasher, recipe.GetDockerfile())
		hashField(hasher, recipe.GetContext())
		if explicitRoots {
			hashField(hasher, recipe.GetContextRoot().String())
		}
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
