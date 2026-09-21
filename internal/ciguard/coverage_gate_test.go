package ciguard

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The coverage threshold is decided twice: in CI by the SHA-pinned
// vladopajic/go-test-coverage action, and locally by `make check-coverage`,
// which installs the same tool as a Go module. Only the first was pinned —
// the Makefile installed @latest — so the local check answered from whatever
// upstream had tagged that day.
//
// That divergence announces itself with nothing: the two versions coincided
// when this guard was written, and the day they stop, `make check-coverage`
// keeps exiting 0 while reporting on a tool CI does not run. Dependabot moves
// the action's SHA and tag comment; nothing moves the Makefile, so a bump has
// to be one reviewed change touching both.

const coverageAction = "vladopajic/go-test-coverage"

const coverageModule = "github.com/vladopajic/go-test-coverage/v2"

var makefileCoveragePin = regexp.MustCompile(`(?m)^GO_TEST_COVERAGE_VERSION\s*[:?]?=\s*(v[0-9]+\.[0-9]+\.[0-9]+)`)

// Every install of the tool has to reach its version through the variable. A
// literal version in a recipe — or a bare @latest — is a tool this guard
// cannot see: the declaration stays equal to the workflow, the test stays
// green, and `make check-coverage` runs something else.
var makefileCoverageInstall = regexp.MustCompile(regexp.QuoteMeta(coverageModule) + `@(\S+)`)

func TestWorkflowCoverageToolMatchesTheMakefile(t *testing.T) {
	makefile, err := os.ReadFile(filepath.Join(repoRoot(t), "Makefile"))
	require.NoError(t, err)

	// Exactly one declaration: make takes the last assignment, this regex takes
	// the first, so a second one is a pin that reads equal to this guard and
	// installs a different version.
	pins := makefileCoveragePin.FindAllSubmatch(makefile, -1)
	require.Len(t, pins, 1,
		"the Makefile must declare GO_TEST_COVERAGE_VERSION exactly once (found %d), "+
			"or `make check-coverage` cannot be pinned to the coverage gate CI runs. "+
			"make uses the last assignment; this guard would read the first.",
		len(pins))
	pinned := string(pins[0][1])

	installs := makefileCoverageInstall.FindAllSubmatch(makefile, -1)
	require.NotEmpty(t, installs,
		"no Makefile recipe installs %s, so GO_TEST_COVERAGE_VERSION pins nothing "+
			"that runs", coverageModule)
	for _, install := range installs {
		require.Equal(t, "$(GO_TEST_COVERAGE_VERSION)", string(install[1]),
			"the Makefile installs %s@%s. Every install must go through "+
				"@$(GO_TEST_COVERAGE_VERSION), or the version `make check-coverage` runs "+
				"is one this guard cannot see.",
			coverageModule, install[1])
	}

	used := 0
	for _, path := range workflowFiles(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)

		for i, line := range strings.Split(string(raw), "\n") {
			ref, ok := actionRef(line)
			if !ok || !strings.HasPrefix(ref, coverageAction+"@") {
				continue
			}
			// TestEveryActionIsPinnedToAFullCommitSHA already requires the
			// `owner/repo@<sha> # <exact tag>` shape, and the tag the comment
			// claims is checked against the SHA it is pinned to, so the comment
			// is the version this action runs.
			m := pinnedUse.FindStringSubmatch(ref)
			require.NotNil(t, m,
				"%s:%d pins %s as %q rather than `owner/repo@<40-char sha> # <exact "+
					"tag>`, so the version CI's coverage gate runs cannot be read",
				filepath.Base(path), i+1, coverageAction, ref)
			used++
			require.Equal(t, pinned, m[3],
				"%s:%d runs the coverage gate at %s but the Makefile installs %s. "+
					"`make check-coverage` would then report on a different tool than the "+
					"gate that decides whether the branch lands; bump both together.",
				filepath.Base(path), i+1, m[3], pinned)
		}
	}
	require.NotZero(t, used,
		"no workflow uses %s, so there is no coverage gate for the Makefile to match",
		coverageAction)
}
