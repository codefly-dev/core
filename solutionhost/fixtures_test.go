package solutionhost_test

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

// A fixture that is shipped but not described, or described but not shipped, is
// worse than a missing one: a consumer testing against it would be testing
// against something this repository does not claim.
func TestEveryShippedFileIsADescribedFixture(t *testing.T) {
	entries, err := fs.ReadDir(solutionhost.FixtureFS(), ".")
	require.NoError(t, err)

	onDisk := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		require.False(t, entry.IsDir())
		name, found := strings.CutSuffix(entry.Name(), ".codefly.yaml")
		require.Truef(t, found, "%s is not a binding document", entry.Name())
		onDisk[name] = struct{}{}
	}

	described := make(map[string]struct{}, len(solutionhost.Fixtures()))
	for _, shipped := range solutionhost.Fixtures() {
		require.NotEmpty(t, shipped.Document)
		described[shipped.Name] = struct{}{}
		if shipped.Outcome == solutionhost.OutcomeAccepted {
			require.NotEmptyf(t, shipped.Decision, "%s must say which decision it expects", shipped.Name)
		} else {
			require.Emptyf(t, shipped.Decision, "%s is rejected, so no decision is reached", shipped.Name)
		}
	}
	require.Equal(t, onDisk, described)
}

func TestFixtureDocumentNamesAMissingFixture(t *testing.T) {
	_, err := solutionhost.FixtureDocument("no-such-fixture")
	require.ErrorContains(t, err, "no-such-fixture")
}

// Every fixture targets the one coordinate FixtureHost reconciles, so a
// consumer never has to guess which host state a fixture is written against.
func TestEveryFixtureTargetsTheFixtureHost(t *testing.T) {
	for _, shipped := range solutionhost.Fixtures() {
		require.Containsf(t, string(shipped.Document), solutionhost.FixtureCoordinate, "%s", shipped.Name)
	}

	host, err := solutionhost.FixtureHost()
	require.NoError(t, err)
	require.Equal(t, solutionhost.FixtureCoordinate, host.Coordinate)
	require.Len(t, host.Applied, 1)
	require.Equal(t, solutionhost.FixtureBindingID, host.Applied[0].Binding)
	require.Equal(t, uint64(4), host.Applied[0].Generation)
}
