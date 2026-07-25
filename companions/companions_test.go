package companions_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/companions"
	"github.com/stretchr/testify/require"
)

// manifestVersion reads companions/<dir>/info.codefly.yaml directly from disk,
// independent of the derivation code, so the test catches any tag that stops
// tracking its manifest.
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
	return info.Version
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
