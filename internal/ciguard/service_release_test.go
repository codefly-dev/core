package ciguard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Agent owners enable archive SBOMs in their GoReleaser configuration. The
// reusable publisher must supply the scanner before invoking GoReleaser.
func TestSharedServiceReleaseInstallsSBOMScanner(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join(repoRoot(t), ".github/workflows/go-service-release.yml"))
	require.NoError(t, err)
	var config workflow
	require.NoError(t, yaml.Unmarshal(payload, &config))
	scanner := false
	publisher := false
	for _, step := range config.Jobs["goreleaser"].Steps {
		if strings.HasPrefix(step.Uses, "anchore/sbom-action/download-syft@") {
			require.Empty(t, step.If, "SBOM scanner installation must not be conditional")
			require.NotEmpty(t, step.With["syft-version"])
			scanner = true
		}
		if strings.HasPrefix(step.Uses, "goreleaser/goreleaser-action@") {
			require.True(t, scanner, "archive SBOMs require Syft before publication")
			publisher = true
		}
	}
	require.True(t, publisher, "the shared workflow must publish the owner configuration")
}
