package golang

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The runner's entries come after the host's: numbered from the host's count,
// so git reads the host's configuration first and the runner's last.
func TestGitMaintenanceOffEnvAppendsAfterTheHostConfiguration(t *testing.T) {
	host := []string{
		"GIT_CONFIG_COUNT=2",
		"GIT_CONFIG_KEY_0=url.https://example.com/.insteadOf",
		"GIT_CONFIG_VALUE_0=https://mirror.example.com/",
		"GIT_CONFIG_KEY_1=maintenance.auto",
		"GIT_CONFIG_VALUE_1=true",
	}
	want := []string{
		"GIT_CONFIG_KEY_2=maintenance.auto",
		"GIT_CONFIG_VALUE_2=false",
		"GIT_CONFIG_KEY_3=gc.auto",
		"GIT_CONFIG_VALUE_3=0",
		"GIT_CONFIG_COUNT=4",
	}
	if got := gitMaintenanceOffEnv(host); !slices.Equal(got, want) {
		t.Fatalf("gitMaintenanceOffEnv(host) = %q, want %q", got, want)
	}
}

func TestGitMaintenanceOffEnvStartsAtZeroWithoutAUsableHostCount(t *testing.T) {
	want := []string{
		"GIT_CONFIG_KEY_0=maintenance.auto",
		"GIT_CONFIG_VALUE_0=false",
		"GIT_CONFIG_KEY_1=gc.auto",
		"GIT_CONFIG_VALUE_1=0",
		"GIT_CONFIG_COUNT=2",
	}
	for _, host := range [][]string{nil, {"GIT_CONFIG_COUNT=nope"}, {"GIT_CONFIG_COUNT=-1"}} {
		if got := gitMaintenanceOffEnv(host); !slices.Equal(got, want) {
			t.Fatalf("gitMaintenanceOffEnv(%q) = %q, want %q", host, got, want)
		}
	}
}

// GoModuleHandling resolving a pseudo-version makes the go tool shallow-fetch
// the commit and then deepen the clone: every ref, then `git fetch --unshallow`.
// The first of those detaches `git maintenance run --auto`, which rewrites
// .git/shallow under the unshallow ("shallow file has changed since we read
// it"). This asserts the fetch under test is that fetch, and that no automatic
// maintenance ran in the shared module cache to race it.
func TestGoModuleHandlingFetchesWithoutGitMaintainingTheModuleCache(t *testing.T) {
	for _, tool := range []string{"go", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("the runner runs the host %s", tool)
		}
	}
	ctx := t.Context()
	const modulePath = "example.com/private/lib.git"
	repository, commit, pseudoVersion := pseudoVersionRepository(t, modulePath)

	gitconfig := filepath.Join(t.TempDir(), "gitconfig")
	writeFile(t, gitconfig, fmt.Sprintf("[url \"file://%s\"]\n\tinsteadOf = https://%s\n\tinsteadOf = https://%s\n",
		repository, modulePath, strings.TrimSuffix(modulePath, ".git")))
	cache := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", gitconfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_TERMINAL_PROMPT", "0")
	t.Setenv("GOPRIVATE", "example.com/*")
	t.Setenv("GOPROXY", "direct")
	t.Setenv("GOSUMDB", "off")
	t.Setenv("GOFLAGS", "-mod=mod -modcacherw")
	t.Setenv("GOMODCACHE", cache)
	t.Cleanup(func() {
		clean := exec.Command("go", "clean", "-modcache")
		clean.Env = append(os.Environ(), "GOMODCACHE="+cache)
		_ = clean.Run()
	})
	trace := filepath.Join(t.TempDir(), "git-trace.json")
	t.Setenv("GIT_TRACE2_EVENT", trace)

	root := t.TempDir()
	source := filepath.Join(root, "code")
	writeFile(t, filepath.Join(source, "go.mod"),
		"module example.com/service\n\ngo 1.22\n\nrequire "+modulePath+" "+pseudoVersion+"\n")

	env, err := NewNativeGoRunner(ctx, root, "code")
	if err != nil {
		t.Fatal(err)
	}
	env.WithLocalCacheDir(t.TempDir())
	t.Cleanup(func() { _ = env.Shutdown(t.Context()) })
	if err := env.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if err := env.GoModuleHandling(ctx); err != nil {
		t.Fatalf("go mod download of %s@%s (%s): %v", modulePath, pseudoVersion, commit, err)
	}

	children := gitChildren(t, trace)
	if !childRuns(children, "fetch", "--depth=1") {
		t.Fatal("the clone must be shallow, or this test is not the fetch that fails")
	}
	if !childRuns(children, "fetch", "--unshallow") {
		t.Fatal("the shallow clone must be deepened, or this test is not the fetch that fails")
	}
	if childRuns(children, "maintenance", "--auto") || childRuns(children, "gc", "--auto") {
		t.Fatal("a git fetch in the module cache detached automatic maintenance that rewrites the clone under the next fetch")
	}
}

// pseudoVersionRepository writes a bare repository whose tip commit descends
// from a tag, so a pseudo-version naming it names a base version the go tool
// must validate by deepening its shallow clone. It returns the repository, the
// commit and that pseudo-version.
func pseudoVersionRepository(t *testing.T, modulePath string) (string, string, string) {
	t.Helper()
	work := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = work
		command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "TZ=UTC",
			"GIT_AUTHOR_NAME=codefly", "GIT_AUTHOR_EMAIL=codefly@example.com",
			"GIT_COMMITTER_NAME=codefly", "GIT_COMMITTER_EMAIL=codefly@example.com")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main", ".")
	writeFile(t, filepath.Join(work, "go.mod"), "module "+modulePath+"\n\ngo 1.22\n")
	writeFile(t, filepath.Join(work, "lib.go"), "package lib\n\nconst Version = 1\n")
	git("add", "-A")
	git("commit", "-q", "-m", "released")
	git("tag", "v0.0.1")
	writeFile(t, filepath.Join(work, "lib.go"), "package lib\n\nconst Version = 2\n")
	git("commit", "-q", "-am", "pinned")
	commit := git("rev-parse", "HEAD")
	stamp := git("log", "-1", "--format=%cd", "--date=format-local:%Y%m%d%H%M%S")

	repository := filepath.Join(t.TempDir(), "lib.git")
	git("clone", "-q", "--bare", work, repository)
	// A commit named by hash is fetched only from a server that allows it, as
	// GitHub does; without this the go tool's clone is never shallow.
	git("-C", repository, "config", "uploadpack.allowAnySHA1InWant", "true")
	return repository, commit, "v0.0.2-0." + stamp + "-" + commit[:12]
}

func writeFile(t *testing.T, name, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// gitChildren is every command line in a GIT_TRACE2_EVENT log.
func gitChildren(t *testing.T, trace string) [][]string {
	t.Helper()
	content, err := os.ReadFile(trace)
	if err != nil {
		t.Fatalf("git wrote no trace, so the fetch under test was not observed: %v", err)
	}
	var children [][]string
	for _, line := range strings.Split(string(content), "\n") {
		var event struct {
			Argv []string `json:"argv"`
		}
		if json.Unmarshal([]byte(line), &event) == nil && len(event.Argv) > 0 {
			children = append(children, event.Argv)
		}
	}
	return children
}

// childRuns reports whether one command line carries every word, the command
// matching whether git ran it by name or by its path in git-core.
func childRuns(children [][]string, words ...string) bool {
	for _, argv := range children {
		if !slices.ContainsFunc(words, func(word string) bool {
			return !slices.ContainsFunc(argv, func(arg string) bool {
				return arg == word || strings.HasSuffix(arg, "/"+word)
			})
		}) {
			return true
		}
	}
	return false
}
