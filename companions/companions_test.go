package companions_test

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/companions"
	"github.com/codefly-dev/core/resources"
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

	// Directory holding the info.codefly.yaml that owns each image tag. The
	// name is also the ghcr package name, so the two cannot drift.
	dirs := []string{"proto", "go", "python", "node", "execution", "codefly"}

	got := map[string]string{}
	for _, img := range images {
		require.Containsf(t, dirs, img.Name, "unexpected embedded image %s", img.FullName())
		require.Equalf(t, resources.ImageRegistry, img.Repository,
			"embedded image %s must be addressed through the canonical registry", img.Name)
		require.Equalf(t, manifestVersion(t, img.Name), img.Tag,
			"embedded tag for %s must match %s/info.codefly.yaml", img.Name, img.Name)
		got[img.Name] = img.Tag
	}

	require.Len(t, got, len(dirs), "every companion image must be enumerated")
}

// The codefly companion is the only image staging a per-architecture binary;
// without the ARG the ${TARGETARCH} in its COPY expands to nothing and the
// build fails rather than silently picking an architecture.
func TestCodeflyCompanionDeclaresTargetArchitecture(t *testing.T) {
	_, filename, _, _ := runtime.Caller(0)
	content, err := os.ReadFile(filepath.Join(filepath.Dir(filename), "codefly", "Dockerfile"))
	require.NoError(t, err)
	require.Contains(t, string(content), "ARG TARGETARCH")
}
