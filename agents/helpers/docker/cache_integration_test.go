package docker

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestMaxCacheDoesNotExportHiddenOrUnusedContext(t *testing.T) {
	if os.Getenv("CODEFLY_TEST_REGISTRY_CACHE") != "1" {
		t.Skip("set CODEFLY_TEST_REGISTRY_CACHE=1 for BuildKit cache inspection")
	}
	root := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(root, "builder"), 0755))
	marker := "codefly-never-export-this-input"
	for name, data := range map[string]string{
		"builder/Dockerfile":   "FROM scratch\nCOPY app /app\n",
		"builder/dockerignore": "builder/\n!secret\n",
		".dockerignore":        "secret\n", "secret": marker, "unused": marker, "app": "application",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(data), 0600))
	}
	prepared, err := PrepareBuildContext(context.Background(), root, "builder/Dockerfile", "builder/dockerignore")
	require.NoError(t, err)
	defer prepared.Close()
	builderName := fmt.Sprintf("codefly-cache-inspection-%d", time.Now().UnixNano())
	created, err := exec.Command("docker", "buildx", "create", "--name", builderName, "--driver", "docker-container").CombinedOutput()
	require.NoError(t, err, "%s", created)
	t.Cleanup(func() {
		output, err := exec.Command("docker", "buildx", "rm", builderName).CombinedOutput()
		require.NoError(t, err, "%s", output)
	})
	t.Setenv("BUILDX_BUILDER", builderName)
	cacheDir := filepath.Join(t.TempDir(), "cache")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	args := []string{"buildx", "build", "--platform", "linux/amd64", "-f", prepared.Dockerfile, "--cache-to", "type=local,dest=" + cacheDir + ",mode=max,compression=gzip", prepared.Root}
	output, err := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
	require.NoError(t, err, "%s", output)
	blobs, err := filepath.Glob(filepath.Join(cacheDir, "blobs", "sha256", "*"))
	require.NoError(t, err)
	inspected := 0
	for _, blob := range blobs {
		data, err := os.ReadFile(blob)
		require.NoError(t, err)
		if !bytes.HasPrefix(data, []byte{0x1f, 0x8b}) {
			continue
		}
		zipped, err := gzip.NewReader(bytes.NewReader(data))
		require.NoError(t, err)
		layer, err := io.ReadAll(zipped)
		require.NoError(t, err)
		require.NoError(t, zipped.Close())
		require.NotContains(t, string(layer), marker, "exported layer %s contains undeclared input", blob)
		inspected++
	}
	require.Positive(t, inspected, "must inspect actual exported filesystem layers")
}

func TestLanguageRegistryCacheAcrossCleanBuilders(t *testing.T) {
	if os.Getenv("CODEFLY_TEST_REGISTRY_CACHE") != "1" {
		t.Skip("set CODEFLY_TEST_REGISTRY_CACHE=1 for language cache integration")
	}
	for _, fixture := range []struct{ name, source, lock, base, newBase string }{
		{"go", "main.go", "go.sum", "golang:1.26-alpine", "golang:1.25-alpine"},
		{"next", "pages/index.js", "package-lock.json", "node:22-alpine", "node:24-alpine"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			defer cancel()
			run := func(args ...string) string {
				t.Helper()
				cmd := exec.CommandContext(ctx, "docker", args...)
				var stderr bytes.Buffer
				cmd.Stderr = &stderr
				out, err := cmd.Output()
				require.NoError(t, err, "%s", stderr.String())
				return strings.TrimSpace(string(out))
			}
			name := fmt.Sprintf("codefly-language-cache-%s-%d", fixture.name, time.Now().UnixNano())
			run("network", "create", name)
			t.Cleanup(func() {
				out, err := exec.Command("docker", "network", "rm", name).CombinedOutput()
				require.NoError(t, err, "%s", out)
			})
			registry := name + ".local"
			phase := os.Getenv("CODEFLY_TEST_CACHE_PHASE")
			state := os.Getenv("CODEFLY_TEST_CACHE_STATE")
			registryArgs := []string{"run", "-d", "--name", registry, "--network", name}
			if phase == "warm" {
				require.NotEmpty(t, state)
				registryArgs = append(registryArgs, "-v", filepath.Join(state, fixture.name)+":/var/lib/registry")
			}
			run(append(registryArgs, "registry:2")...)
			t.Cleanup(func() {
				out, err := exec.Command("docker", "rm", "-f", registry).CombinedOutput()
				require.NoError(t, err, "%s", out)
			})
			root := filepath.Join(t.TempDir(), "source")
			require.NoError(t, os.CopyFS(root, os.DirFS(filepath.Join("testdata", "cache", fixture.name))))
			config := filepath.Join(t.TempDir(), "buildkitd.toml")
			require.NoError(t, os.WriteFile(config, []byte(fmt.Sprintf("[registry.\"%s:5000\"]\n  http = true\n", registry)), 0600))
			policy := &builderv0.BuildCacheOptions{Backend: "registry", Scope: "workspace/" + fixture.name + "/app/trusted", Imports: []string{registry + ":5000/cache"}, Exports: []string{registry + ":5000/cache"}}
			first, last := 0, 4
			if phase == "cold" {
				last = 0
			}
			if phase == "warm" {
				first = 1
			}
			previousReceipt := ""
			if phase == "warm" {
				data, err := os.ReadFile(filepath.Join(state, fixture.name+".receipt"))
				require.NoError(t, err)
				previousReceipt = string(data)
			}
			for i := first; i <= last; i++ {
				builderName := fmt.Sprintf("%s-%d", name, i)
				run("buildx", "create", "--name", builderName, "--driver", "docker-container", "--driver-opt", "network="+name, "--buildkitd-config", config)
				t.Setenv("BUILDX_BUILDER", builderName)
				t.Cleanup(func() {
					out, err := exec.Command("docker", "buildx", "rm", builderName).CombinedOutput()
					require.NoError(t, err, "%s", out)
				})
				if i == 1 {
					p := filepath.Join(root, fixture.source)
					data, err := os.ReadFile(p)
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(p, bytes.ReplaceAll(data, []byte("source-v1"), []byte("source-v2")), 0600))
				}
				if i == 2 {
					p := filepath.Join(root, fixture.lock)
					f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0600)
					require.NoError(t, err)
					_, err = f.WriteString("\n")
					require.NoError(t, err)
					require.NoError(t, f.Close())
				}
				if i >= 3 {
					p := filepath.Join(root, "Dockerfile")
					data, err := os.ReadFile(p)
					require.NoError(t, err)
					if i == 3 {
						data = bytes.ReplaceAll(data, []byte(fixture.base), []byte(fixture.newBase))
					} else {
						data = bytes.ReplaceAll(data, []byte("BUILD_INPUT=one"), []byte("BUILD_INPUT=two"))
					}
					require.NoError(t, os.WriteFile(p, data, 0600))
				}
				var output bytes.Buffer
				tag := fmt.Sprintf("%s:%d", name, i)
				t.Cleanup(func() {
					out, err := exec.Command("docker", "image", "rm", tag).CombinedOutput()
					require.NoError(t, err, "%s", out)
				})
				builder, err := NewBuilder(BuilderConfiguration{Root: root, Dockerfile: "Dockerfile", Destination: resources.NewDockerImage(tag), Output: &output, Platform: "linux/amd64", Cache: policy})
				require.NoError(t, err)
				result, err := builder.Build(ctx)
				require.NoError(t, err, "%s", output.String())
				t.Logf("%s run=%d wall=%s\n%s", fixture.name, i, result.Duration, output.String())
				id := run("create", "--platform", "linux/amd64", tag, "unused")
				receiptPath := filepath.Join(t.TempDir(), "receipt")
				run("cp", id+":/dependency-receipt", receiptPath)
				run("rm", id)
				receipt, err := os.ReadFile(receiptPath)
				require.NoError(t, err)
				require.NotEmpty(t, receipt)
				if i > 0 {
					// A cached filesystem retains the install receipt; executing RUN mints a new one.
					hit := string(receipt) == previousReceipt
					require.Equal(t, i == 1 || i == 4, hit, "dependency-install reuse for run %d", i)
					t.Logf("%s run=%d dependency_cache_hit=%t", fixture.name, i, hit)
				}
				previousReceipt = string(receipt)
				wantSource := "source-v1"
				if i > 0 {
					wantSource = "source-v2"
				}
				wantInput := "one"
				if i == 4 {
					wantInput = "two"
				}
				if fixture.name == "go" {
					content := run("run", "--rm", "--platform", "linux/amd64", tag)
					require.Contains(t, content, wantSource)
					require.Contains(t, content, wantInput)
				} else {
					id := run("create", "--platform", "linux/amd64", tag, "unused")
					out := filepath.Join(t.TempDir(), "index.html")
					run("cp", id+":/out/index.html", out)
					run("rm", id)
					content, err := os.ReadFile(out)
					require.NoError(t, err)
					require.Contains(t, string(content), wantSource)
					require.Contains(t, string(content), wantInput)
				}
			}
			if phase == "cold" {
				require.NotEmpty(t, state)
				require.NoError(t, os.MkdirAll(state, 0755))
				run("cp", registry+":/var/lib/registry", filepath.Join(state, fixture.name))
				require.NoError(t, os.WriteFile(filepath.Join(state, fixture.name+".receipt"), []byte(previousReceipt), 0600))
			}
		})
	}
}
