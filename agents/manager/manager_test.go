package manager

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
)

// makeAgent returns a service agent stub with the given publisher and name.
func makeAgent(publisher, name string) *resources.Agent {
	return &resources.Agent{
		Kind:      resources.ServiceAgent,
		Publisher: publisher,
		Name:      name,
	}
}

// touchFile creates an empty file at the given path.
func touchFile(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create file %s: %v", path, err)
	}
	f.Close()
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
