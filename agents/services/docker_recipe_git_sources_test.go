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

	// A host that knows only v5 expects v5 for these recipes and refuses.
	plan.ContractVersion = DockerBuildRecipeGoModulesContractVersion
	require.ErrorContains(t, VerifyDockerBuildPlan(dir, plan), "contract")
	plan.ContractVersion = DockerBuildRecipeGitSourcesContractVersion
	require.NoError(t, VerifyDockerBuildPlan(dir, plan))

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
	require.ErrorContains(t, err, "also a Go module proxy context")
}

func TestRecipeGitSourceDownloadsAreValidated(t *testing.T) {
	dir := recipeDir(t)
	for name, mutate := range map[string]func(*builderv0.GitSourceDownload){
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

	// Accepted forms: ssh with a login name and a port, a SHA-256 commit, a
	// percent-escape, and the same repository locked at two commits.
	recipe := gitSourceRecipe()
	recipe.GitSourceDownloads = append(recipe.GitSourceDownloads,
		&builderv0.GitSourceDownload{Repository: "ssh://git@git.example.com:2222/team/lib.git", Commit: lockedCommit, MirrorContext: "gitsources"},
		&builderv0.GitSourceDownload{Repository: "https://git.example.com/team/my+lib~v1_x-y", Commit: strings.Repeat("b", 64), MirrorContext: "gitsources"},
		&builderv0.GitSourceDownload{Repository: "https://git.example.com/team/lib", Commit: strings.Repeat("c", 40), MirrorContext: "gitsources"})
	_, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.NoError(t, err)

	recipe.GitSourceDownloads = append(recipe.GitSourceDownloads, gitSource())
	_, err = BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
	require.ErrorContains(t, err, "twice")
}

// A refused repository may be a credential pasted into a URL. The error names
// the declaration and the reason, and never the URL or any part of it, because
// a builder's error reaches the caller's RPC status and logs.
func TestRecipeGitSourceRepositoryRefusalsNeverEchoTheURL(t *testing.T) {
	dir := recipeDir(t)
	for name, repository := range map[string]string{
		"empty":                "",
		"http":                 "http://git.example.com/team/lib",
		"file":                 "file:///srv/lib",
		"ext":                  "ext::sh -c touch% /tmp/x",
		"git+ssh":              "git+ssh://git@git.example.com/team/lib",
		"uppercase SSH":        "SSH://git@git.example.com/team/lib",
		"mixed-case https":     "Https://git.example.com/team/lib",
		"scp-like":             "git@git.example.com:team/lib.git",
		"https token":          "https://s3cr3tvalue@git.example.com/team/lib",
		"https password":       "https://user:s3cr3tvalue@git.example.com/team/lib",
		"ssh password":         "ssh://git:s3cr3tvalue@git.example.com/team/lib",
		"query token":          "https://git.example.com/team/lib?token=s3cr3tvalue",
		"empty query":          "https://git.example.com/team/lib?",
		"fragment":             "https://git.example.com/team/lib#" + lockedCommit,
		"no host":              "https:///team/lib",
		"no path":              "https://git.example.com",
		"root path":            "https://git.example.com/",
		"bad port":             "https://git.example.com:http/team/lib",
		"bad escape":           "https://git.example.com/team/%zzlib",
		"space":                "https://git.example.com/team/my lib",
		"double quote":         `https://git.example.com/team/"lib`,
		"single quote":         "https://git.example.com/team/'lib",
		"backquote":            "https://git.example.com/team/`id`",
		"command substitution": "https://git.example.com/team/$(id)",
		"variable":             "https://git.example.com/team/${HOME}",
		"semicolon":            "https://git.example.com/team/lib;id",
		"ampersand":            "https://git.example.com/team/lib&id",
		"pipe":                 "https://git.example.com/team/lib|id",
		"redirect":             "https://git.example.com/team/lib>out",
		"backslash":            `https://git.example.com/team\lib`,
		"newline":              "https://git.example.com/team/lib\nRUN id",
		"tab":                  "https://git.example.com/team/\tlib",
		"glob":                 "https://git.example.com/team/*",
		"brackets":             "https://git.example.com/team/[lib]",
		"equals":               "https://git.example.com/team/a=b",
		"comma":                "https://git.example.com/team/a,b",
		"non-ascii":            "https://git.example.com/team/lib\u00e9",
		"ssh user quote":       "ssh://g'it@git.example.com/team/lib",
		"host underscore":      "https://git_example.com/team/lib",
	} {
		t.Run(name, func(t *testing.T) {
			recipe := gitSourceRecipe()
			recipe.GitSourceDownloads[0].Repository = repository
			_, err := BuildDockerBuildPlan(dir, []*builderv0.DockerBuildRecipe{recipe})
			require.ErrorContains(t, err, "git source 0: repository")
			require.NotContains(t, err.Error(), "s3cr3tvalue")
			require.NotContains(t, err.Error(), "git.example.com")
		})
	}
}

// git decodes percent-encoding before it splits an ssh URL into user, host and
// path, so an encoded bracket or "@" could move the host past the checks. Such
// URLs are refused both when a plan is built and when one is verified.
func TestRecipeGitSourceRepositoryRefusesEncodedAuthorityTricks(t *testing.T) {
	for name, repository := range map[string]string{
		"ssh encoded brackets": "ssh://git.example.com/team/other@%5B::1%5D/repo",
		"ssh @ in path":        "ssh://git.example.com/team/other@evil.example/repo",
		"ssh encoded @":        "ssh://git@git.example.com/team/other%40evil/repo",
		"https encoded slash":  "https://git.example.com/team%2F..%2Fother/lib",
		"https encoded @":      "https://git.example.com/team/other%40evil/lib",
		"https encoded [":      "https://git.example.com/team/%5B::1%5D/lib",
		"https encoded colon":  "https://git.example.com/team/a%3Ab",
		"https encoded %":      "https://git.example.com/team/a%25b",
		"https @ in path":      "https://git.example.com/team/other@evil.example/lib",
		"ssh port too large":   "ssh://git.example.com:65536/team/repo",
		"https port zero":      "https://git.example.com:0/team/lib",
		"ssh login dash":       "ssh://-git@git.example.com/team/repo",
		"ssh host dash":        "ssh://-host.example/team/repo",
	} {
		t.Run(name, func(t *testing.T) {
			dir := recipeDir(t)
			source := gitSource()
			source.Repository = repository
			_, err := SingleImageBuildPlan(dir, "example/app:v1", RecipeBuildPlatforms(), []string{"Dockerfile"}, WithGitSourceDownloads(source))
			require.ErrorContains(t, err, "git source 0: repository")

			plan, err := SingleImageBuildPlan(dir, "example/app:v1", RecipeBuildPlatforms(), []string{"Dockerfile"}, WithGitSourceDownloads(gitSource()))
			require.NoError(t, err)
			plan.GetRecipes()[0].GetGitSourceDownloads()[0].Repository = repository
			err = VerifyDockerBuildPlan(dir, plan)
			require.ErrorContains(t, err, "git source 0: repository")
			require.NotContains(t, err.Error(), "example")
		})
	}
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
	} {
		mirror := GitSourceMirrorPath(repository)
		require.NotContains(t, seen, mirror, "%s collides with %s", repository, seen[mirror])
		seen[mirror] = repository
	}
}

func TestGitSourceMirrorRef(t *testing.T) {
	require.Equal(t, "refs/commit/"+lockedCommit, GitSourceMirrorRef(lockedCommit))
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
