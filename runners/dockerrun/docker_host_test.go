package dockerrun

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeContext lays out a docker context on disk exactly as the CLI does:
// contexts/meta/<sha256(name)>/meta.json with the docker endpoint host.
func writeContext(t *testing.T, configDir, name, host string) {
	t.Helper()
	id := fmt.Sprintf("%x", sha256.Sum256([]byte(name)))
	dir := filepath.Join(configDir, "contexts", "meta", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := map[string]any{
		"Name": name,
		"Endpoints": map[string]any{
			"docker": map[string]any{"Host": host},
		},
	}
	data, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeCurrentContext(t *testing.T, configDir, name string) {
	t.Helper()
	data, err := json.Marshal(map[string]any{"currentContext": name})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestResolveDockerHost(t *testing.T) {
	// DOCKER_HOST wins over everything.
	t.Run("DOCKER_HOST env takes precedence", func(t *testing.T) {
		t.Setenv("DOCKER_HOST", "tcp://1.2.3.4:2375")
		t.Setenv("DOCKER_CONTEXT", "")
		got := resolveDockerHost()
		if got.Host != "tcp://1.2.3.4:2375" || got.Source != "DOCKER_HOST env" {
			t.Fatalf("got %+v", got)
		}
	})

	// The OrbStack case from the issue: no DOCKER_HOST, active context points at
	// a non-default socket — must resolve to that socket, not /var/run/docker.sock.
	t.Run("active context from config.json", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("DOCKER_HOST", "")
		t.Setenv("DOCKER_CONTEXT", "")
		t.Setenv("DOCKER_CONFIG", configDir)
		writeContext(t, configDir, "orbstack", "unix:///Users/me/.orbstack/run/docker.sock")
		writeCurrentContext(t, configDir, "orbstack")

		got := resolveDockerHost()
		if got.Host != "unix:///Users/me/.orbstack/run/docker.sock" {
			t.Fatalf("host = %q", got.Host)
		}
		if got.Source != `docker context "orbstack"` {
			t.Fatalf("source = %q", got.Source)
		}
	})

	// DOCKER_CONTEXT overrides config.json's currentContext.
	t.Run("DOCKER_CONTEXT env selects the context", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("DOCKER_HOST", "")
		t.Setenv("DOCKER_CONFIG", configDir)
		writeContext(t, configDir, "colima", "unix:///Users/me/.colima/docker.sock")
		writeCurrentContext(t, configDir, "orbstack")
		t.Setenv("DOCKER_CONTEXT", "colima")

		got := resolveDockerHost()
		if got.Host != "unix:///Users/me/.colima/docker.sock" {
			t.Fatalf("host = %q", got.Host)
		}
	})

	// The "default" context is DOCKER_HOST-based; it has no meta.json, so we
	// must fall back to the default socket rather than error.
	t.Run("default context falls back to default socket", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("DOCKER_HOST", "")
		t.Setenv("DOCKER_CONTEXT", "")
		t.Setenv("DOCKER_CONFIG", configDir)
		writeCurrentContext(t, configDir, "default")

		got := resolveDockerHost()
		if got.Host != defaultDockerHost || got.Source != "default socket" {
			t.Fatalf("got %+v", got)
		}
	})

	// No env, no config → default socket.
	t.Run("no config falls back to default socket", func(t *testing.T) {
		configDir := t.TempDir()
		t.Setenv("DOCKER_HOST", "")
		t.Setenv("DOCKER_CONTEXT", "")
		t.Setenv("DOCKER_CONFIG", configDir)

		got := resolveDockerHost()
		if got.Host != defaultDockerHost || got.Source != "default socket" {
			t.Fatalf("got %+v", got)
		}
	})
}
