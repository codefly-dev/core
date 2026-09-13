package docker

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
)

// recordedDocker puts a fake `docker` on PATH that appends each invocation's
// arguments to a log and succeeds, so the argv this package builds can be
// asserted without a daemon, a registry, or a real buildx builder.
func recordedDocker(t *testing.T) func() []string {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "argv")
	script := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + log + "\nexit 0\n"
	require.NoError(t, os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755))
	t.Setenv("PATH", bin)
	return func() []string {
		contents, err := os.ReadFile(log)
		require.NoError(t, err)
		var invocations []string
		for _, line := range strings.Split(strings.TrimSpace(string(contents)), "\n") {
			if line != "" {
				invocations = append(invocations, line)
			}
		}
		return invocations
	}
}

func buildInvocation(t *testing.T, invocations []string) string {
	t.Helper()
	for _, invocation := range invocations {
		if strings.HasPrefix(invocation, "buildx build") {
			return invocation
		}
	}
	require.FailNow(t, "no `docker buildx build` invocation was recorded", "recorded: %v", invocations)
	return ""
}

func backendRequest() backendBuildRequest {
	return backendBuildRequest{
		Platform:   "linux/amd64",
		Dockerfile: "/tmp/svc/builder/Dockerfile",
		Tag:        "repo/app:test",
		Context:    "/tmp/svc",
		Output:     &bytes.Buffer{},
	}
}

// The caller provisions its own Buildx builder and names it in the request; the
// build must run on that builder and not on whatever ambient builder the host
// has selected. Nothing else in this repository asserts that --builder reaches
// the docker CLI, so a regression here would otherwise surface only as a build
// running on the wrong builder — silently succeeding against the wrong registry
// network, or failing to export cache at all.
func TestBuildxBuilderSelectionReachesTheDockerCLI(t *testing.T) {
	recorded := recordedDocker(t)

	request := backendRequest()
	request.BuildxBuilder = "codefly-provisioned"
	require.NoError(t, dockerCLIBackend{}.Build(t.Context(), request))

	invocation := buildInvocation(t, recorded())
	require.Contains(t, invocation, "--builder codefly-provisioned")
	require.Contains(t, invocation, "--platform linux/amd64")
	require.Contains(t, invocation, "-t repo/app:test")
}

// Without an explicit selection the flag must be absent entirely: passing
// `--builder ""` would fail, and passing a default would silently override the
// operator's own buildx selection.
func TestBuildOmitsBuilderFlagWithoutSelection(t *testing.T) {
	recorded := recordedDocker(t)

	require.NoError(t, dockerCLIBackend{}.Build(t.Context(), backendRequest()))

	require.NotContains(t, buildInvocation(t, recorded()), "--builder")
}

// Cache policy and builder selection are independent: selecting a builder must
// not drop the cache arguments, which is the combination the registry-cache
// conformance job exercises against a real registry.
func TestBuildCarriesCacheArgumentsAlongsideBuilderSelection(t *testing.T) {
	recorded := recordedDocker(t)

	request := backendRequest()
	request.BuildxBuilder = "codefly-provisioned"
	request.Cache = &builderv0.BuildCacheOptions{
		Backend: "registry", Scope: "workspace/service/app",
		Imports: []string{"registry.example.com/cache"},
		Exports: []string{"registry.example.com/cache"},
	}
	require.NoError(t, dockerCLIBackend{}.Build(t.Context(), request))

	invocation := buildInvocation(t, recorded())
	require.Contains(t, invocation, "--builder codefly-provisioned")
	require.Contains(t, invocation, "--cache-from")
	require.Contains(t, invocation, "--cache-to")
}
