package dockerrun

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/mount"
	"github.com/docker/go-connections/nat"
)

func TestWithPersistentCacheMountScopesStateByEnvironmentName(t *testing.T) {
	home := t.TempDir()
	t.Setenv(resources.CodeflyHomeEnv, home)

	dev := &DockerEnvironment{name: "codefly-workspace-frontend-dev"}
	stable := &DockerEnvironment{name: "codefly-workspace-frontend-stable"}
	target := "/workspace/frontend/.next"

	devCache, err := dev.WithPersistentCacheMount(context.Background(), "next-build", target)
	if err != nil {
		t.Fatalf("dev cache mount: %v", err)
	}
	stableCache, err := stable.WithPersistentCacheMount(context.Background(), "next-build", target)
	if err != nil {
		t.Fatalf("stable cache mount: %v", err)
	}

	if devCache == stableCache {
		t.Fatalf("namespaced environments shared cache path %q", devCache)
	}
	if want := filepath.Join(home, "runtime-cache", dev.name, "next-build"); devCache != want {
		t.Fatalf("dev cache = %q, want %q", devCache, want)
	}
	if len(dev.mounts) != 1 {
		t.Fatalf("dev mounts = %d, want 1", len(dev.mounts))
	}
	if got := dev.mounts[0]; got.Type != mount.TypeBind || got.Source != devCache || got.Target != target {
		t.Fatalf("dev mount = %#v, want bind %q -> %q", got, devCache, target)
	}
}

func TestWithPersistentCacheMountRejectsUnsafeKeysAndTargets(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	env := &DockerEnvironment{name: "codefly-workspace-frontend-dev"}

	for _, key := range []string{"", ".", "..", "../escape", "nested/cache", `nested\cache`} {
		if _, err := env.WithPersistentCacheMount(context.Background(), key, "/workspace/.next"); err == nil {
			t.Errorf("key %q was accepted", key)
		}
	}
	if _, err := env.WithPersistentCacheMount(context.Background(), "next-build", ".next"); err == nil {
		t.Fatal("relative target was accepted")
	}
	if len(env.mounts) != 0 {
		t.Fatalf("invalid requests added %d mounts", len(env.mounts))
	}
}

func TestContainerConfigFingerprintDetectsReusableRuntimeDrift(t *testing.T) {
	httpPort := nat.Port("3000/tcp")
	config := &container.Config{
		Image:      "codeflydev/node:0.0.12",
		User:       "1000:1000",
		Env:        []string{"PUBLIC=value", "SECRET=do-not-store-in-label"},
		Cmd:        []string{"sleep", "infinity"},
		WorkingDir: "/workspace",
		Tty:        false,
		ExposedPorts: nat.PortSet{
			httpPort: struct{}{},
		},
		Labels: map[string]string{LabelCodeflyEphemeral: "true"},
	}
	host := &container.HostConfig{
		Mounts: []mount.Mount{{
			Type:   mount.TypeBind,
			Source: "/host/source",
			Target: "/workspace",
		}},
		PortBindings: nat.PortMap{
			httpPort: []nat.PortBinding{{HostIP: "127.0.0.1", HostPort: "10571"}},
		},
	}

	original, err := containerConfigFingerprint(config, host)
	if err != nil {
		t.Fatalf("fingerprint original config: %v", err)
	}
	repeated, err := containerConfigFingerprint(config, host)
	if err != nil {
		t.Fatalf("fingerprint repeated config: %v", err)
	}
	if original != repeated {
		t.Fatalf("unchanged config fingerprint drifted: %q != %q", original, repeated)
	}

	withNodeModules := *host
	withNodeModules.Mounts = append(append([]mount.Mount(nil), host.Mounts...), mount.Mount{
		Type:   mount.TypeBind,
		Source: "/codefly/cache/node-modules",
		Target: "/workspace/node_modules",
	})
	changedMount, err := containerConfigFingerprint(config, &withNodeModules)
	if err != nil {
		t.Fatalf("fingerprint changed mounts: %v", err)
	}
	if changedMount == original {
		t.Fatal("adding a runtime cache mount did not change the fingerprint")
	}

	changedConfig := *config
	changedConfig.Env = append([]string(nil), config.Env...)
	changedConfig.Env[0] = "PUBLIC=changed"
	changedEnvironment, err := containerConfigFingerprint(&changedConfig, host)
	if err != nil {
		t.Fatalf("fingerprint changed environment: %v", err)
	}
	if changedEnvironment == original {
		t.Fatal("changing the runtime environment did not change the fingerprint")
	}
}
