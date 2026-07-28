package companions_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/companions"
	"github.com/stretchr/testify/require"
)

// manifestVersion reads companions/<dir>/info.codefly.yaml directly from disk,
// independent of the derivation code, so the test catches any tag that stops
// tracking its manifest. It applies the same semver normalization the
// derivation does, so a non-canonical manifest version doesn't spuriously
// fail the comparison.
func manifestVersion(t *testing.T, dir string) string {
	t.Helper()
	_, filename, _, _ := runtime.Caller(0)
	path := filepath.Join(filepath.Dir(filename), dir, "info.codefly.yaml")
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	var info struct {
		Version string `yaml:"version"`
	}
	require.NoError(t, yaml.Unmarshal(content, &info))
	v, err := semver.NewVersion(info.Version)
	require.NoError(t, err)
	return v.String()
}

func TestEmbeddedDerivesEveryTagFromManifest(t *testing.T) {
	ctx := context.Background()

	images, err := companions.Embedded(ctx)
	require.NoError(t, err)

	// Directory holding the info.codefly.yaml that owns each image tag.
	dirByName := map[string]string{
		"codeflydev/proto":     "proto",
		"codeflydev/go":        "go",
		"codeflydev/python":    "python",
		"codeflydev/node":      "node",
		"codeflydev/execution": "execution",
		"codeflydev/codefly":   "codefly",
	}

	got := map[string]string{}
	for _, img := range images {
		dir, ok := dirByName[img.Name]
		require.Truef(t, ok, "unexpected embedded image %s", img.Name)
		require.Equalf(t, manifestVersion(t, dir), img.Tag,
			"embedded tag for %s must match %s/info.codefly.yaml", img.Name, dir)
		got[img.Name] = img.Tag
	}

	require.Len(t, got, len(dirByName), "every companion image must be enumerated")
}

func TestNodeCompanionPreservesTargetArchitecture(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	root := filepath.Dir(filename)

	codeflyDockerfile, err := os.ReadFile(filepath.Join(root, "codefly", "Dockerfile"))
	require.NoError(t, err)
	require.Contains(t, string(codeflyDockerfile), "ARG TARGETARCH")
	require.Contains(t, string(codeflyDockerfile), "bin/linux/${TARGETARCH}/codefly")

	nodeDockerfile, err := os.ReadFile(filepath.Join(root, "node", "Dockerfile"))
	require.NoError(t, err)
	codeflyVersion := strings.TrimSpace(string(mustReadFile(t, filepath.Join(root, "codefly", "info.codefly.yaml"))))
	require.Equal(t, "version: 0.0.4", codeflyVersion)
	require.Contains(t, string(nodeDockerfile), "codeflydev/codefly:0.0.4")
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	content, err := os.ReadFile(path)
	require.NoError(t, err)
	return content
}
