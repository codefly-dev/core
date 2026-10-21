package services

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
)

const lockedCommit = "0123456789abcdef0123456789abcdef01234567"

func gitSource() *builderv0.GitSourceDownload {
	return &builderv0.GitSourceDownload{Repository: "https://git.example.com/team/lib", Commit: lockedCommit, MirrorContext: "gitsources"}
}

func gitSourceRecipe() *builderv0.DockerBuildRecipe {
	return &builderv0.DockerBuildRecipe{
		Name: "app", Dockerfile: "Dockerfile", Context: ".", Image: "example/app:v1",
		GitSourceDownloads: []*builderv0.GitSourceDownload{gitSource()},
	}
}

func recipeDir(t *testing.T) string {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Dockerfile"), []byte("FROM scratch\n"), 0o644))
	return dir
}

func TestRecipeGitSourceDownloadsVersionAndIntegrity(t *testing.T) {
	dir := recipeDir(t)
	recipe := gitSourceRecipe()
	plan, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeGitSourcesContractVersion, plan.GetContractVersion())
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))

	// A host that predates the field expects v3 for these recipes and refuses.
	plan.ContractVersion = DockerBuildRecipeContractVersion
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "contract")
	plan.ContractVersion = DockerBuildRecipeGitSourcesContractVersion

	// Every field of the declaration is covered by the digest.
	source := recipe.GitSourceDownloads[0]
	for _, mutate := range []func(*builderv0.GitSourceDownload){
		func(s *builderv0.GitSourceDownload) { s.Repository = "https://git.example.com/team/other" },
		func(s *builderv0.GitSourceDownload) { s.Commit = strings.Repeat("a", 40) },
		func(s *builderv0.GitSourceDownload) { s.MirrorContext = "elsewhere" },
	} {
		mutate(source)
		require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "digest")
		recipe.GitSourceDownloads[0] = gitSource()
		source = recipe.GitSourceDownloads[0]
	}
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))

	// Adding a second source, or dropping the declaration and the version, is detected.
	recipe.GitSourceDownloads = append(recipe.GitSourceDownloads, &builderv0.GitSourceDownload{
		Repository: "ssh://git@git.example.com/team/tool.git", Commit: lockedCommit, MirrorContext: "gitsources"})
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "digest")
	recipe.GitSourceDownloads = nil
	plan.ContractVersion = DockerBuildRecipeContractVersion
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "digest")
}

func TestRecipeGitSourceDownloadsWithGoModules(t *testing.T) {
	dir := recipeDir(t)
	recipe := gitSourceRecipe()
	recipe.GoModuleDownloads = []*builderv0.GoModuleDownload{{ModuleRoot: "code", ProxyContext: "gomodproxy"}}
	recipe.ContextRoot = builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_OUTPUT
	plan, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeGitSourcesContractVersion, plan.GetContractVersion())

	// v6 still digests what v4 and v5 introduced.
	recipe.GoModuleDownloads[0].ModuleRoot = "other"
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "digest")
	recipe.GoModuleDownloads[0].ModuleRoot = "code"
	recipe.ContextRoot = builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_SERVICE
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "digest")

	// One build context cannot be both a module proxy and a mirror set.
	recipe.ContextRoot = builderv0.RecipeContextRoot_RECIPE_CONTEXT_ROOT_OUTPUT
	recipe.GitSourceDownloads[0].MirrorContext = "gomodproxy"
	_, err = BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.ErrorContains(t, err, "both")
}

func TestRecipeGitSourceDownloadsAreValidated(t *testing.T) {
	dir := recipeDir(t)
	for name, mutate := range map[string]func(*builderv0.GitSourceDownload){
		"empty repository": func(s *builderv0.GitSourceDownload) { s.Repository = "" },
		"http scheme":      func(s *builderv0.GitSourceDownload) { s.Repository = "http://git.example.com/team/lib" },
		"file scheme":      func(s *builderv0.GitSourceDownload) { s.Repository = "file:///srv/lib" },
		"ext scheme":       func(s *builderv0.GitSourceDownload) { s.Repository = "ext::sh -c touch% /tmp/x" },
		"scp-like":         func(s *builderv0.GitSourceDownload) { s.Repository = "git@git.example.com:team/lib.git" },
		"https token":      func(s *builderv0.GitSourceDownload) { s.Repository = "https://token@git.example.com/team/lib" },
		"https password":   func(s *builderv0.GitSourceDownload) { s.Repository = "https://user:secret@git.example.com/team/lib" },
		"ssh password":     func(s *builderv0.GitSourceDownload) { s.Repository = "ssh://git:secret@git.example.com/team/lib" },
		"no host":          func(s *builderv0.GitSourceDownload) { s.Repository = "https:///team/lib" },
		"no path":          func(s *builderv0.GitSourceDownload) { s.Repository = "https://git.example.com/" },
		"query":            func(s *builderv0.GitSourceDownload) { s.Repository = "https://git.example.com/team/lib?rev=main" },
		"empty query":      func(s *builderv0.GitSourceDownload) { s.Repository = "https://git.example.com/team/lib?" },
		"fragment": func(s *builderv0.GitSourceDownload) {
			s.Repository = "https://git.example.com/team/lib#" + lockedCommit
		},
		"short commit":        func(s *builderv0.GitSourceDownload) { s.Commit = lockedCommit[:12] },
		"uppercase commit":    func(s *builderv0.GitSourceDownload) { s.Commit = strings.ToUpper(lockedCommit) },
		"41-char commit":      func(s *builderv0.GitSourceDownload) { s.Commit = lockedCommit + "0" },
		"branch as commit":    func(s *builderv0.GitSourceDownload) { s.Commit = "main" },
		"empty context":       func(s *builderv0.GitSourceDownload) { s.MirrorContext = "" },
		"uppercase context":   func(s *builderv0.GitSourceDownload) { s.MirrorContext = "GitSources" },
		"context with a path": func(s *builderv0.GitSourceDownload) { s.MirrorContext = "git/sources" },
	} {
		t.Run(name, func(t *testing.T) {
			recipe := gitSourceRecipe()
			mutate(recipe.GitSourceDownloads[0])
			_, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
			require.Error(t, err)
		})
	}

	// Accepted forms: ssh with a login name, git+ssh, a SHA-256 commit, and the
	// same repository locked at two commits.
	recipe := gitSourceRecipe()
	recipe.GitSourceDownloads = append(recipe.GitSourceDownloads,
		&builderv0.GitSourceDownload{Repository: "ssh://git@git.example.com/team/lib.git", Commit: lockedCommit, MirrorContext: "gitsources"},
		&builderv0.GitSourceDownload{Repository: "git+ssh://git@git.example.com/team/lib.git", Commit: strings.Repeat("b", 64), MirrorContext: "gitsources"},
		&builderv0.GitSourceDownload{Repository: "https://git.example.com/team/lib", Commit: strings.Repeat("c", 40), MirrorContext: "gitsources"})
	_, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.NoError(t, err)

	recipe.GitSourceDownloads = append(recipe.GitSourceDownloads, gitSource())
	_, err = BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.ErrorContains(t, err, "twice")
}

func TestGitSourceMirrorPath(t *testing.T) {
	path := GitSourceMirrorPath("https://git.example.com/team/lib")
	require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{64}\.git$`), path)
	// The layout the proto documents, so a caller in any language agrees.
	sum := sha256.Sum256([]byte("https://git.example.com/team/lib"))
	require.Equal(t, hex.EncodeToString(sum[:])+".git", path)
	require.Equal(t, path, GitSourceMirrorPath("https://git.example.com/team/lib"))
	// The URL as declared is the key: near-identical URLs never share a mirror.
	seen := map[string]string{}
	for _, repository := range []string{
		"https://git.example.com/team/lib",
		"https://git.example.com/team/lib.git",
		"https://git.example.com/team/lib/",
		"https://git.example.com/Team/lib",
		"ssh://git@git.example.com/team/lib",
		"git+ssh://git@git.example.com/team/lib",
	} {
		mirror := GitSourceMirrorPath(repository)
		require.NotContains(t, seen, mirror, "%s collides with %s", repository, seen[mirror])
		seen[mirror] = repository
	}
}

func TestSingleImageBuildPlanDeclaresGitSourceDownloads(t *testing.T) {
	dir := recipeDir(t)
	plan, err := SingleImageBuildPlan(dir, "example/app:v1", RecipeBuildPlatforms(), []string{"Dockerfile"},
		WithGitSourceDownloads(gitSource()))
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeGitSourcesContractVersion, plan.GetContractVersion())
	require.Equal(t, lockedCommit, plan.GetRecipes()[0].GetGitSourceDownloads()[0].GetCommit())
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))

	// The builder's single-image response threads the options through.
	builder := &BuilderWrapper{Base: &Base{loaded: true}}
	builder.Builder = builder
	resp, err := builder.SingleImageBuildResponse(&builderv0.BuildRequest{OutputDirectory: dir}, "example/app:v1", []string{"Dockerfile"},
		WithGitSourceDownloads(gitSource()))
	require.NoError(t, err)
	require.Equal(t, DockerBuildRecipeGitSourcesContractVersion, resp.GetResult().GetDockerBuildPlan().GetContractVersion())
}
