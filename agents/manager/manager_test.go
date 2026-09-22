package manager

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestExecutableIdentityUsesContentNotPathOrTimestamps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent")
	require.NoError(t, os.WriteFile(path, []byte("first"), 0o755))
	original, info, err := executableIdentity(t.Context(), path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte("other"), 0o755))
	require.NoError(t, os.Chtimes(path, info.ModTime(), info.ModTime()))
	changed, _, err := executableIdentity(t.Context(), path)
	require.NoError(t, err)
	require.NotEqual(t, original, changed)
	link := filepath.Join(t.TempDir(), "agent")
	require.NoError(t, os.Symlink(path, link))
	linked, _, err := executableIdentity(t.Context(), link)
	require.NoError(t, err)
	require.Equal(t, changed, linked)
	require.NoError(t, os.Chmod(path, 0o644))
	_, _, err = executableIdentity(t.Context(), path)
	require.ErrorContains(t, err, "not a regular executable")
	_, _, err = executableIdentity(t.Context(), t.TempDir())
	require.ErrorContains(t, err, "not a regular executable")
	fifo := filepath.Join(t.TempDir(), "fifo")
	output, err := exec.CommandContext(t.Context(), "mkfifo", fifo).CombinedOutput()
	require.NoError(t, err, "%s", output)
	require.NoError(t, os.Chmod(fifo, 0o755))
	_, _, err = executableIdentity(t.Context(), fifo)
	require.ErrorContains(t, err, "not a regular executable")
}

// makeAgent returns a service agent stub with the given publisher and name.
func makeAgent(publisher, name string) *resources.Agent {
	return &resources.Agent{
		Kind:      resources.ServiceAgent,
		Publisher: publisher,
		Name:      name,
	}
}

// touchFile creates an empty executable at the given path.
func touchFile(t *testing.T, path string) {
	t.Helper()
	err := os.WriteFile(path, nil, 0o755)
	if err != nil {
		t.Fatalf("create file %s: %v", path, err)
	}
}

func TestFindLocalLatest_SingleVersion(t *testing.T) {
	dir := t.TempDir()
	agent := makeAgent("codefly.dev", "go-grpc")

	touchFile(t, filepath.Join(dir, "go-grpc__0.1.0"))

	err := findLocalLatestInDir(dir, agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agent.Version != "0.1.0" {
		t.Errorf("expected version 0.1.0, got %s", agent.Version)
	}
}

func TestFindLocalLatest_MultipleVersions(t *testing.T) {
	dir := t.TempDir()
	agent := makeAgent("codefly.dev", "go-grpc")

	touchFile(t, filepath.Join(dir, "go-grpc__0.1.0"))
	touchFile(t, filepath.Join(dir, "go-grpc__1.2.3"))
	touchFile(t, filepath.Join(dir, "go-grpc__0.9.9"))

	err := findLocalLatestInDir(dir, agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agent.Version != "1.2.3" {
		t.Errorf("expected version 1.2.3, got %s", agent.Version)
	}
}

func TestFindLocalLatest_NoMatchingFiles(t *testing.T) {
	t.Run("empty directory", func(t *testing.T) {
		dir := t.TempDir()
		agent := makeAgent("codefly.dev", "go-grpc")

		err := findLocalLatestInDir(dir, agent)
		if err == nil {
			t.Fatal("expected error for empty directory, got nil")
		}
	})

	t.Run("files with wrong prefix", func(t *testing.T) {
		dir := t.TempDir()
		agent := makeAgent("codefly.dev", "go-grpc")

		touchFile(t, filepath.Join(dir, "other-agent__1.0.0"))
		touchFile(t, filepath.Join(dir, "random-file"))

		err := findLocalLatestInDir(dir, agent)
		if err == nil {
			t.Fatal("expected error when no files match prefix, got nil")
		}
	})

	t.Run("nonexistent directory", func(t *testing.T) {
		agent := makeAgent("codefly.dev", "go-grpc")

		err := findLocalLatestInDir("/tmp/nonexistent-dir-for-test", agent)
		if err == nil {
			t.Fatal("expected error for nonexistent directory, got nil")
		}
	})
}

func TestFindLocalLatest_InvalidSemver(t *testing.T) {
	dir := t.TempDir()
	agent := makeAgent("codefly.dev", "go-grpc")

	// Files that match the prefix but have non-semver suffixes.
	touchFile(t, filepath.Join(dir, "go-grpc__notaversion"))
	touchFile(t, filepath.Join(dir, "go-grpc__1.2"))
	touchFile(t, filepath.Join(dir, "go-grpc__abc.def.ghi"))
	touchFile(t, filepath.Join(dir, "go-grpc__"))

	err := findLocalLatestInDir(dir, agent)
	if err == nil {
		t.Fatal("expected error when all versions are invalid semver, got nil")
	}
}

func TestFindLocalLatest_MixedValidInvalid(t *testing.T) {
	dir := t.TempDir()
	agent := makeAgent("codefly.dev", "go-grpc")

	// Valid versions.
	touchFile(t, filepath.Join(dir, "go-grpc__0.2.0"))
	touchFile(t, filepath.Join(dir, "go-grpc__1.0.0"))

	// Invalid versions (should be skipped).
	touchFile(t, filepath.Join(dir, "go-grpc__notaversion"))
	touchFile(t, filepath.Join(dir, "go-grpc__1.2"))
	touchFile(t, filepath.Join(dir, "go-grpc__"))

	// Unrelated files (should be skipped).
	touchFile(t, filepath.Join(dir, "other-agent__9.9.9"))

	err := findLocalLatestInDir(dir, agent)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if agent.Version != "1.0.0" {
		t.Errorf("expected version 1.0.0, got %s", agent.Version)
	}
}

func TestAgentSourceLocal(t *testing.T) {
	// Save and restore the env so the test is hermetic.
	prev, had := os.LookupEnv(AgentSourceEnv)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(AgentSourceEnv, prev)
		} else {
			_ = os.Unsetenv(AgentSourceEnv)
		}
	})

	cases := []struct {
		val  string
		want bool
	}{
		{"local", true},
		{"LOCAL", true},
		{"Local", true},
		{"remote", false},
		{"", false},
		{"random", false},
	}
	for _, c := range cases {
		_ = os.Setenv(AgentSourceEnv, c.val)
		if got := AgentSourceLocal(); got != c.want {
			t.Errorf("AgentSourceLocal() with %s=%q = %v, want %v",
				AgentSourceEnv, c.val, got, c.want)
		}
	}
	_ = os.Unsetenv(AgentSourceEnv)
	if AgentSourceLocal() {
		t.Error("AgentSourceLocal() should be false when env is unset")
	}
}

func TestFindLocalLatestUsesToolboxAndProviderRegistryRoutes(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	for _, kind := range []resources.AgentKind{resources.ToolboxAgent, resources.ProviderAgent} {
		agent := &resources.Agent{Kind: kind, Publisher: "codefly.dev", Name: "fixture", Version: "latest"}
		registration, err := resources.AgentKindRegistrationFor(kind)
		if err != nil {
			t.Fatal(err)
		}
		dir := filepath.Join(resources.AgentBase(context.Background()), "agents", registration.InstallSubdirectory, agent.Publisher)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		touchFile(t, filepath.Join(dir, "fixture__1.2.3"))
		if err := FindLocalLatest(context.Background(), agent); err != nil {
			t.Fatal(err)
		}
		if agent.Version != "1.2.3" {
			t.Fatalf("%s resolved version %q", kind, agent.Version)
		}
	}
}

func TestFindLocalLatestRejectsTraversalBeforeReadingTheCache(t *testing.T) {
	home := t.TempDir()
	t.Setenv(resources.CodeflyHomeEnv, home)
	outside := filepath.Join(home, "outside")
	require.NoError(t, os.Mkdir(outside, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "peer__9.0.0"), nil, 0o755))
	for _, registration := range resources.AgentKindRegistry() {
		for _, component := range []string{"publisher", "name", "version"} {
			t.Run(string(registration.Resource)+"/"+component, func(t *testing.T) {
				selected := resources.Agent{Kind: registration.Resource, Publisher: "example.test", Name: "peer", Version: "latest"}
				switch component {
				case "publisher":
					selected.Publisher = "../../outside"
				case "name":
					selected.Name = "../../../peer"
				case "version":
					selected.Version = "../1.0.0"
				}
				before := selected
				err := FindLocalLatest(t.Context(), &selected)
				require.ErrorContains(t, err, "invalid agent "+component)
				require.Equal(t, before, selected)
			})
		}
	}
}

func TestFindLocalLatestSelectsAnExecutableNotADirectoryOrPartialFile(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "peer__1.0.0"), nil, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "peer__3.0.0"), nil, 0o600))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "peer__4.0.0"), 0o755))
	require.NoError(t, os.Symlink("peer__1.0.0", filepath.Join(dir, "peer__2.0.0")))
	selected := makeAgent("example.test", "peer")
	require.NoError(t, findLocalLatestInDir(dir, selected))
	require.Equal(t, "2.0.0", selected.Version)
}

func TestProviderGitHubResolutionFailsBeforeNetwork(t *testing.T) {
	agent := &resources.Agent{
		Kind: resources.ProviderAgent, Publisher: "codefly.dev", Name: "fixture", Version: "1.2.3",
	}
	if err := Download(context.Background(), agent); err == nil {
		t.Fatal("provider GitHub auto-download must be disabled")
	}
	if _, err := PinToLatestRelease(context.Background(), agent); err == nil {
		t.Fatal("provider GitHub release resolution must be disabled")
	}
}

// installAgentBuild puts an executable build of agent into a temporary codefly
// home, as `codefly agent build` would, and returns nothing: the home is set
// for the duration of the test.
func installAgentBuild(t *testing.T, agent *resources.Agent, version string) {
	t.Helper()
	registration, err := resources.AgentKindRegistrationFor(agent.Kind)
	require.NoError(t, err)
	dir := filepath.Join(resources.AgentBase(t.Context()), "agents", registration.InstallSubdirectory, agent.Publisher)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	touchFile(t, filepath.Join(dir, agent.Name+"__"+version))
}

// stubLatestReleaseTag makes the release lookup return tag without network.
func stubLatestReleaseTag(t *testing.T, tag string) {
	t.Helper()
	previous := latestReleaseTag
	latestReleaseTag = func(context.Context, string, string) (string, error) { return tag, nil }
	t.Cleanup(func() { latestReleaseTag = previous })
}

func TestSelectsLatest(t *testing.T) {
	require.True(t, SelectsLatest("latest"))
	require.True(t, SelectsLatest(""))
	require.False(t, SelectsLatest("0.1.43"))
}

func TestResolveLatestTreatsAnOmittedVersionAsLatest(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	t.Setenv(AgentSourceEnv, "")
	stubLatestReleaseTag(t, "v0.1.44")

	agent := makeAgent("codefly.dev", "go-grpc")
	agent.Version = ""

	source, err := ResolveLatest(t.Context(), agent)
	require.NoError(t, err)
	require.Equal(t, "github", source)
	require.Equal(t, "0.1.44", agent.Version)
}

func TestResolveLatestPrefersThePublishedReleaseOverAnInstalledBuild(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	t.Setenv(AgentSourceEnv, "")
	agent := makeAgent("codefly.dev", "go-grpc")
	agent.Version = "latest"
	installAgentBuild(t, agent, "0.1.43")
	stubLatestReleaseTag(t, "v0.1.44")

	source, err := ResolveLatest(t.Context(), agent)
	require.NoError(t, err)
	require.Equal(t, "github", source)
	require.Equal(t, "0.1.44", agent.Version)
}

func TestResolveLatestUsesAnInstalledBuildOnlyWhenItIsSelected(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	t.Setenv(AgentSourceEnv, "local")
	agent := makeAgent("codefly.dev", "go-grpc")
	agent.Version = "latest"
	installAgentBuild(t, agent, "0.1.43")
	stubLatestReleaseTag(t, "v0.1.44")

	source, err := ResolveLatest(t.Context(), agent)
	require.NoError(t, err)
	require.Equal(t, "local", source)
	require.Equal(t, "0.1.43", agent.Version)
}

func TestResolveLatestResolvesLocallyForKindsThatPublishNoReleases(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	t.Setenv(AgentSourceEnv, "")
	agent := &resources.Agent{Kind: resources.ProviderAgent, Publisher: "codefly.dev", Name: "llm", Version: "latest"}
	installAgentBuild(t, agent, "0.2.7")

	source, err := ResolveLatest(t.Context(), agent)
	require.NoError(t, err)
	require.Equal(t, "local", source)
	require.Equal(t, "0.2.7", agent.Version)
}

func TestResolveLatestLeavesAConcreteVersionPinned(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	t.Setenv(AgentSourceEnv, "")
	agent := makeAgent("codefly.dev", "go-grpc")
	agent.Version = "0.1.43"

	source, err := ResolveLatest(t.Context(), agent)
	require.NoError(t, err)
	require.Equal(t, "pinned", source)
	require.Equal(t, "0.1.43", agent.Version)
}
