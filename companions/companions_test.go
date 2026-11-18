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
	companionindex "github.com/codefly-dev/core/companions/index"
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

// In a test binary with no registered index, the images are the recipes of
// this checkout: the tags track their manifests.
func TestImagesTrackTheRecipesInATestBinary(t *testing.T) {
	ctx := context.Background()

	images, err := companions.Images(ctx)
	require.NoError(t, err)

	// Directory holding the info.codefly.yaml that owns each image tag. The
	// name is also the ghcr package name, so the two cannot drift.
	dirs := []string{"proto", "go", "python", "node", "codefly"}

	got := map[string]string{}
	for _, img := range images {
		require.Containsf(t, dirs, img.Name, "unexpected image %s", img.FullName())
		require.Equalf(t, resources.ImageRegistry, img.Repository,
			"image %s must be addressed through the canonical registry", img.Name)
		require.Equalf(t, manifestVersion(t, img.Name), img.Tag,
			"tag for %s must match %s/info.codefly.yaml", img.Name, img.Name)
		got[img.Name] = img.Tag
	}

	require.Len(t, got, len(dirs), "every companion image must be enumerated")
}

// The caller's index decides every tag: core compiles none in.
func TestTheRegisteredIndexDecidesEveryTag(t *testing.T) {
	index := companionindex.Index{"proto": "9.0.1", "go": "9.0.2", "python": "9.0.3", "node": "9.0.4", "codefly": "9.0.6"}
	require.NoError(t, companionindex.Register(index))
	t.Cleanup(func() { require.NoError(t, companionindex.Register(nil)) })

	images, err := companions.Images(context.Background())
	require.NoError(t, err)
	require.Len(t, images, len(index))
	for _, img := range images {
		require.Equalf(t, index[img.Name], img.Tag, "tag for %s", img.Name)
	}
	specs, err := companions.BuildSpecs()
	require.NoError(t, err)
	for _, spec := range specs {
		require.Equalf(t, index[spec.Name], spec.Version, "build spec version for %s", spec.Name)
	}
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
