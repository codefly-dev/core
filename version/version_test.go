package version_test

import (
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/version"
)

// canonical is the same shape scripts/check_version_tag.sh enforces: a bare
// x.y.z that maps 1:1 onto a vX.Y.Z git tag. Keeping the two in agreement is
// the point — a value one accepts and the other rejects would let drift back in.
var canonical = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// TestVersionLoads is the guard that was missing entirely: nothing in the repo
// read this package, so a malformed info.codefly.yaml surfaced only as a panic
// from resources' init() (resources/agent.go calls version.Version through
// shared.Must), taking down every binary in the ecosystem at process start.
// Failing here means it never gets that far.
func TestVersionLoads(t *testing.T) {
	value, err := version.Version(context.Background())
	require.NoError(t, err, "version/info.codefly.yaml must parse; resources' init() panics otherwise")
	require.Regexp(t, canonical, value)
}

// TestVersionMatchesEmbeddedFile pins Version() to the file on disk. The
// //go:embed copy is resolved at compile time, so an edit that never made it
// into a rebuilt binary would otherwise pass unnoticed.
func TestVersionMatchesEmbeddedFile(t *testing.T) {
	content, err := os.ReadFile("info.codefly.yaml")
	require.NoError(t, err)

	declared := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(content)), "version:"))
	require.Regexp(t, canonical, declared,
		"info.codefly.yaml must hold a bare x.y.z so it maps onto tag v%s", declared)

	value, err := version.Version(context.Background())
	require.NoError(t, err)
	require.Equal(t, declared, value, "embedded version must match info.codefly.yaml on disk")
}
