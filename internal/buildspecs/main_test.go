package main

import (
	"testing"

	"github.com/codefly-dev/core/companions"
	"github.com/stretchr/testify/require"
)

// The workflow consumes these fields by name; a rename or a dropped field
// silently empties a build input rather than failing the run.
func TestEntriesCarryEveryBuildInputTheWorkflowConsumes(t *testing.T) {
	specs, err := companions.BuildSpecs()
	require.NoError(t, err)

	got := entries(specs)
	require.Len(t, got, len(specs))
	for index, e := range got {
		spec := specs[index]
		require.Equal(t, spec.Name, e.Companion)
		require.Equal(t, spec.Version, e.Version)
		require.Equal(t, spec.Dockerfile, e.Dockerfile)
		require.Equal(t, spec.Context, e.Context)
		require.Equal(t, spec.CLIBinary, e.CLIBinary)
		require.Equal(t, spec.Base, e.Base)
		require.NotEmpty(t, e.Platforms)
	}
}

// buildx takes --platform as one comma-separated value, so the join must not
// introduce spaces.
func TestPlatformsJoinAsBuildxAcceptsThem(t *testing.T) {
	specs, err := companions.BuildSpecs()
	require.NoError(t, err)

	for _, e := range entries(specs) {
		require.NotContains(t, e.Platforms, " ")
	}
}
