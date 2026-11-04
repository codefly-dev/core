package main

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The proxy responses under testdata/proxy are RECORDED from
// proxy.golang.org — the real @v/list for github.com/codefly-dev/core (461
// versions) and the real @v/<version>.info for the versions these fixtures
// pin. No part of the proxy's behaviour is imagined here: the version
// ordering, the publication times every threshold is measured against, and
// the 404 that distinguishes "core never published this" from "core published
// this and you are behind" are all the proxy's own answers.
//
// The repositories under testdata/repos are real go.mod files, in the shapes
// the nine repositories in codefly-dev/core#891 actually have — nested under
// modules/<name>/services/<svc>/code, pseudo-versioned, indirect, replaced.
// The comparisons under testdata/compare are recorded from api.github.com the
// same way, keeping the counters this gate reads (`status`, `ahead_by`,
// `behind_by`, `total_commits`) and dropping the commit and file lists, which
// it does not read and which run to ~18KB each.
//
// The recordings are a deliberately FROZEN snapshot: core published v0.19.19
// while this was being written, and re-recording would churn every number
// asserted below for no gain. The live behaviour against a moving catalogue is
// exercised separately, by running the checker against the nine real
// repositories.
func proxy(t *testing.T) string {
	t.Helper()
	proxyRoot, err := filepath.Abs(filepath.Join("testdata", "proxy"))
	require.NoError(t, err)
	compareRoot, err := filepath.Abs(filepath.Join("testdata", "compare"))
	require.NoError(t, err)

	// One server for both services: the module proxy's @v/ paths and the
	// GitHub API's compare paths do not collide.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const (
			proxyPrefix   = "/github.com/codefly-dev/core/@v/"
			comparePrefix = "/repos/codefly-dev/core/compare/"
		)
		switch {
		case strings.HasPrefix(r.URL.Path, proxyPrefix):
			name := strings.TrimPrefix(r.URL.Path, proxyPrefix)
			// A 404 is the proxy's real answer for a version it does not know,
			// which this gate reads rather than treating as an error. An
			// absent recording is exactly that case.
			body, err := os.ReadFile(filepath.Join(proxyRoot, filepath.Base(name)))
			if err != nil {
				http.Error(w, "not found: "+name, http.StatusNotFound)
				return
			}
			_, _ = w.Write(body)

		case strings.HasPrefix(r.URL.Path, comparePrefix):
			basehead := strings.TrimPrefix(r.URL.Path, comparePrefix)
			_, sha, ok := strings.Cut(basehead, "...")
			if !ok {
				http.Error(w, "unexpected basehead "+basehead, http.StatusBadRequest)
				return
			}
			body, err := os.ReadFile(filepath.Join(compareRoot, filepath.Base(sha)+".json"))
			if err != nil {
				http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
				return
			}
			_, _ = w.Write(body)

		default:
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusBadRequest)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL
}

// at is the instant the fleet was measured in codefly-dev/core#891, so the ages
// these tests assert are the ages the issue's own table would have reported.
var at = time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)

func check(t *testing.T, repo string, now time.Time) report {
	t.Helper()
	shared := proxy(t)
	result, err := run(context.Background(), options{
		root:       filepath.Join("testdata", "repos", repo),
		configPath: defaultConfigPath,
		proxy:      shared,
		api:        shared,
		now:        now,
	})
	require.NoError(t, err)
	return result
}

// byFile indexes a report's findings so a test names the module it means.
func byFile(t *testing.T, r report) map[string]finding {
	t.Helper()
	out := map[string]finding{}
	for _, found := range r.findings {
		out[found.goMod.file] = found
	}
	return out
}

// EVERY go.mod is judged, not the first one found.
//
// This is the defect the gate exists to avoid repeating: codefly-dev/core#891's
// table reports nine repositories at five versions because it read one go.mod
// per repository. The obin-ai modules pin core in
// modules/<name>/services/<svc>/code/go.mod and nowhere near the root, and
// obin-ai/module-robin spans v0.5.11 to v0.19.11 across eleven of them.
func TestEveryNestedGoModIsJudged(t *testing.T) {
	result := check(t, "fleet", at)

	require.Equal(t, 5, result.scanned, "all five go.mod files must be found")
	require.Len(t, result.findings, 4, "the four that pin core must each be judged")

	found := byFile(t, result)
	require.Contains(t, found, "modules/alpha/services/one/code/go.mod")
	require.Contains(t, found, "modules/alpha/services/two/code/go.mod")
	require.Contains(t, found, "clients/go/go.mod")
	require.Contains(t, found, "tools/profiles/go.mod")
	require.NotContains(t, found, "go.mod", "the root module does not pin core")
}

// The numbers in the message are the thing a reader acts on, so they are
// asserted rather than the verdict alone: a gate whose message says "out of
// date" and nothing else is one that gets ignored.
func TestEachFindingCarriesThePinTheCountAndTheAge(t *testing.T) {
	result := check(t, "fleet", at)
	found := byFile(t, result)

	current := found["modules/alpha/services/one/code/go.mod"]
	require.Equal(t, kindCurrent, current.kind)
	require.Equal(t, levelOK, current.level)
	require.Equal(t, 0, current.behind)
	require.Equal(t, "v0.19.18", result.latest)

	behind := found["modules/alpha/services/two/code/go.mod"]
	require.Equal(t, kindBehind, behind.kind)
	require.Equal(t, levelWarn, behind.level, "6 days is past the 3-day warning and inside the 10-day failure")
	require.Equal(t, "v0.19.13", behind.pin)
	require.Equal(t, 5, behind.behind)
	require.Equal(t, "v0.19.14", behind.missing)
	require.Equal(t, 6, int(behind.staleFor.Hours())/24)
	require.Contains(t, behind.describe(), "v0.19.13")
	require.Contains(t, behind.describe(), "5 version(s)")
	require.Contains(t, behind.describe(), "6 days")

	stale := found["tools/profiles/go.mod"]
	require.Equal(t, levelFail, stale.level, "23 days is past the 10-day failure")
	require.Equal(t, 19, stale.behind)
	require.Equal(t, "v0.19.0", stale.missing)
	require.Contains(t, stale.describe(), "23 days")
	require.True(t, result.failed())

	// The remedy names the command that owns the pin, not a `go get`: the CLI
	// owns the fleet verbs, and a hand edit leaves the nested fixtures behind.
	require.Contains(t, stale.remedy, "codefly agent deps --pin v0.19.18")
}

// An indirect requirement still decides which core compiles in — minimum
// version selection takes the maximum across the graph — so it is judged like
// any other pin. Only the remedy differs, because bumping it directly is not
// the fix.
func TestAnIndirectRequirementIsJudgedButGetsADifferentRemedy(t *testing.T) {
	found := byFile(t, check(t, "fleet", at))["clients/go/go.mod"]

	require.True(t, found.goMod.indirect)
	require.Equal(t, kindUntagged, found.kind)
	require.Equal(t, levelWarn, found.level)
	require.Equal(t, "v0.19.7", found.missing)
	require.Contains(t, found.describe(), "an indirect requirement")
	require.Contains(t, found.remedy, "INDIRECT")
	require.NotContains(t, found.remedy, "codefly agent deps --pin")
}

// A pin to a version core never published must not read as up to date.
//
// This is the case codefly-dev/core#891 points at: codefly-dev/cli's table
// entry was v0.19.19 while core's newest tag was v0.19.18, and nothing — not
// the tag list, not the releases API, not the module proxy — resolved
// v0.19.19. A checker comparing version numbers would have called that pin
// NEWER than core and therefore fine, while `go mod download` could not fetch
// it at all.
func TestAVersionCoreNeverPublishedIsAFailureAndNotAheadness(t *testing.T) {
	result := check(t, "unpublished", at)
	require.Len(t, result.findings, 1)
	found := result.findings[0]

	require.Equal(t, kindUnpublished, found.kind)
	require.Equal(t, levelFail, found.level)
	require.True(t, result.failed())
	require.Contains(t, found.describe(), "v0.99.99")
	require.Contains(t, found.describe(), "is not a version core has published")
	require.Contains(t, found.describe(), "above core's newest release v0.19.18",
		"the message has to say the pin sorts above the newest release, which is "+
			"precisely why a numeric comparison would have passed it")
	require.Contains(t, found.remedy, "pin a version that exists")
}

// A pseudo-version ahead of every release is a warning, not a pass: nothing
// states what it contains, and core squash-merges.
func TestAPseudoVersionAheadOfEveryReleaseWarnsRatherThanPasses(t *testing.T) {
	result := check(t, "untagged-ahead", at)
	found := result.findings[0]

	require.Equal(t, kindUntagged, found.kind)
	require.Equal(t, levelWarn, found.level)
	require.Equal(t, 0, found.behind, "nothing is above it")
	require.False(t, result.failed(), "ahead of every release does not fail the gate")
	require.Contains(t, found.describe(), "pseudo-version")
	require.Contains(t, found.describe(), "be256d5c567d")
	require.Contains(t, found.describe(), "ahead of core's newest release v0.19.18")
}

// A pseudo-version naming a commit the proxy can no longer resolve fails
// whatever the dates say. Core squash-merges, so a commit reachable from a
// branch when it was pinned is not reachable from main afterwards, and the pin
// then fetches for nobody — including a fresh CI checkout.
func TestAnOrphanedPseudoVersionFails(t *testing.T) {
	result := check(t, "orphaned", at)
	found := result.findings[0]

	require.Equal(t, kindOrphaned, found.kind)
	require.Equal(t, levelFail, found.level)
	require.Contains(t, found.describe(), "cannot resolve")
	require.Contains(t, found.describe(), "squash-merges")
}

// A replace to a filesystem path means the require line is not what builds, so
// reading the pin would be answering about the wrong core.
func TestALocalReplaceFailsRatherThanReadingThePin(t *testing.T) {
	result := check(t, "local-replace", at)
	found := result.findings[0]

	require.Equal(t, kindLocalReplace, found.kind)
	require.Equal(t, levelFail, found.level,
		"the require line says v0.19.18, which is current — the replacement is "+
			"what makes this a failure")
	require.Contains(t, found.describe(), "filesystem path")
	require.Contains(t, found.remedy, "codefly agent deps --unlink")
}

// A replace to another VERSION is what builds, so that version is what gets
// judged — not the require line above it.
func TestAVersionReplacementIsWhatGetsJudged(t *testing.T) {
	result := check(t, "version-replace", at)
	found := result.findings[0]

	require.Equal(t, "v0.18.3", found.pin, "the replacement, not the v0.19.18 require")
	require.Equal(t, kindBehind, found.kind)
	require.Equal(t, levelFail, found.level)
	require.Contains(t, found.describe(), "replaced to")
}

// A large NUMBER of versions behind does not fail on its own, and that is the
// deliberate shape rather than a gap.
//
// "More than N releases behind" is the obvious gate and it does not survive
// core's cadence: the densest ten-day window in core's history published 63
// versions and the densest three-day window published 30. A count low enough
// to mean anything therefore fires on a repository three days behind during
// an ordinary burst, which trains people to ignore red — and a count high
// enough to clear a burst is one the 10-day rule failed long before.
//
// v0.7.0 has 40 stable versions above it. A day after the first of them was
// published, this repository is 40 versions behind and perfectly current.
func TestManyVersionsBehindDoesNotFailWhileTheUpdateIsStillFresh(t *testing.T) {
	// v0.7.1, the oldest version v0.7.0 is missing, was published 2026-09-30.
	fresh := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	found := check(t, "many-versions-behind", fresh).findings[0]

	require.Equal(t, 40, found.behind, "forty versions behind")
	require.Equal(t, levelOK, found.level, "and not failing, because nothing has been available for long")
	require.Contains(t, found.describe(), "40 version(s)", "the count is still reported")

	// The same pin, measured once the 10-day window has passed, does fail —
	// so what decides is the age and only the age.
	late := time.Date(2026, 10, 15, 0, 0, 0, 0, time.UTC)
	stale := check(t, "many-versions-behind", late).findings[0]
	require.Equal(t, 40, stale.behind, "the same count")
	require.Equal(t, levelFail, stale.level, "a different verdict, from the age alone")
}

// A vendor tree carries the dependency's own manifest. Reading it would report
// a pin the repository does not control.
func TestAVendoredManifestIsNotReadAsAPin(t *testing.T) {
	result := check(t, "vendored", at)

	require.Len(t, result.findings, 1)
	require.Equal(t, "go.mod", result.findings[0].goMod.file)
	require.Equal(t, 1, result.scanned, "the vendor tree is not walked at all")
}

// A repository where nothing pins core must not report green. Green there is
// indistinguishable from a checkout that failed, a path argument that pointed
// at the wrong directory, or a walk that found nothing — and all three are how
// a gate reports success having checked nothing.
func TestARepositoryWithNoCorePinIsRefusedRatherThanPassed(t *testing.T) {
	shared := proxy(t)
	_, err := run(context.Background(), options{
		root:       filepath.Join("testdata", "repos", "no-core"),
		configPath: defaultConfigPath,
		proxy:      shared,
		api:        shared,
		now:        at,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "no go.mod")
	require.Contains(t, err.Error(), "Refusing to report green")
}

// writeConfig puts an exemption file into a copy of a fixture repository, so
// the fixtures themselves stay as they are on disk.
func withConfig(t *testing.T, repo, config string) string {
	t.Helper()
	source := filepath.Join("testdata", "repos", repo)
	destination := t.TempDir()
	require.NoError(t, os.CopyFS(destination, os.DirFS(source)))
	if config != "" {
		require.NoError(t, os.MkdirAll(filepath.Join(destination, ".github"), 0o755))
		require.NoError(t, os.WriteFile(
			filepath.Join(destination, filepath.FromSlash(defaultConfigPath)),
			[]byte(config), 0o644))
	}
	return destination
}

func runAt(t *testing.T, root string, now time.Time) (report, error) {
	t.Helper()
	shared := proxy(t)
	return run(context.Background(), options{
		root:       root,
		configPath: defaultConfigPath,
		proxy:      shared,
		api:        shared,
		now:        now,
	})
}

// An exemption stops a module failing and keeps it in the report, with the
// reason it recorded.
func TestAnExemptionSpaarsAModuleAndCarriesItsReason(t *testing.T) {
	root := withConfig(t, "fleet", `exempt:
  - path: tools/profiles
    reason: pinned with the profile format, bumped with it
`)
	result, err := runAt(t, root, at)
	require.NoError(t, err)

	found := byFile(t, result)["tools/profiles/go.mod"]
	require.Equal(t, kindExempt, found.kind)
	require.Equal(t, levelOK, found.level)
	require.Contains(t, found.describe(), "pinned with the profile format")
	require.False(t, result.failed(), "the only failing module was exempted")
	require.Len(t, result.findings, 4, "an exempt module is still reported")
}

// A "/..." suffix exempts a subtree, which is what a repository with a tree of
// fixtures needs. It is the only pattern accepted: a general glob would let one
// entry quietly cover modules added later.
func TestAnExemptionCanCoverASubtree(t *testing.T) {
	root := withConfig(t, "fleet", `exempt:
  - path: modules/...
    reason: the alpha module is bumped on its own release train
`)
	result, err := runAt(t, root, at)
	require.NoError(t, err)

	found := byFile(t, result)
	require.Equal(t, kindExempt, found["modules/alpha/services/one/code/go.mod"].kind)
	require.Equal(t, kindExempt, found["modules/alpha/services/two/code/go.mod"].kind)
	require.NotEqual(t, kindExempt, found["tools/profiles/go.mod"].kind,
		"the subtree pattern must not reach outside the subtree")
}

// An exemption with no reason is refused. The gate's whole value is that a
// reader knows what to change; an exemption nobody can explain is the same
// hole with the check removed.
func TestAnExemptionWithoutAReasonIsRefused(t *testing.T) {
	root := withConfig(t, "fleet", `exempt:
  - path: tools/profiles
`)
	_, err := runAt(t, root, at)
	require.Error(t, err)
	require.Contains(t, err.Error(), "has no reason")
}

// An exemption matching nothing is refused: it is a recorded reason nobody is
// holding to anything, and the module it meant to cover may have moved under a
// name that is now unguarded.
func TestAStaleExemptionIsRefused(t *testing.T) {
	root := withConfig(t, "fleet", `exempt:
  - path: modules/alpha/services/three/code
    reason: this service was deleted two releases ago
`)
	_, err := runAt(t, root, at)
	require.Error(t, err)
	require.Contains(t, err.Error(), "match no go.mod")
	require.Contains(t, err.Error(), "modules/alpha/services/three/code")
}

// An unknown key in the exemption file is refused rather than ignored. A typo
// in `reason` that decoded silently would produce an exemption with no reason
// at all, which the rule above exists to prevent.
func TestAnUnknownKeyInTheConfigIsRefused(t *testing.T) {
	root := withConfig(t, "fleet", `exempt:
  - path: tools/profiles
    because: a typo for reason
`)
	_, err := runAt(t, root, at)
	require.Error(t, err)
	require.Contains(t, err.Error(), "parse")
}

// A fixture is reported and still fails, but the advice points at the
// exemption rather than at a bump: bumping a fixture breaks the test that
// asserts against its contents.
func TestAFixturePathIsAdvisedToExemptRatherThanBump(t *testing.T) {
	require.True(t, isFixturePath("pkg/prerelease/testdata/clean-module/clients/go"))
	require.False(t, isFixturePath("modules/alpha/services/one/code"))

	remedy := remedyFor(goMod{dir: "pkg/prerelease/testdata/m", file: "x"}, "v0.19.18")
	require.Contains(t, remedy, defaultConfigPath)
	require.Contains(t, remedy, "breaks the test")
}

// The thresholds themselves, stated as a table so the numbers this gate
// enforces are readable in one place. The argument for them is in
// docs/core-pin-drift.md.
func TestTheThresholdsAreTheDocumentedOnes(t *testing.T) {
	require.Equal(t, 3*24*time.Hour, warnAfter)
	require.Equal(t, 10*24*time.Hour, failAfter)

	day := 24 * time.Hour
	cases := []struct {
		name     string
		staleFor time.Duration
		want     level
	}{
		{"nothing behind", 0, levelOK},
		{"two days behind is quiet", 2 * day, levelOK},
		{"three days behind warns", 3 * day, levelWarn},
		{"nine days behind still only warns", 9 * day, levelWarn},
		{"ten days behind fails", 10 * day, levelFail},
		{"a fortnight behind fails", 14 * day, levelFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, levelFor(tc.staleFor))
		})
	}
}

// The clock is on the oldest MISSING version, not on the pin, so bumping to
// something that is not the newest does not reset it. A gate that read the
// pin's own age would report a repository that jumped from v0.18.3 to v0.19.0
// as a day behind, when the version it skipped has been available for weeks.
func TestTheClockRunsFromTheOldestMissingVersionNotThePin(t *testing.T) {
	found := byFile(t, check(t, "fleet", at))["tools/profiles/go.mod"]

	require.Equal(t, "v0.18.3", found.pin)
	require.Equal(t, "v0.19.0", found.missing)
	require.Equal(t, 23, int(found.staleFor.Hours())/24,
		"23 days is the age of v0.19.0, the oldest version this pin is missing")

	pinnedAgo := int(at.Sub(found.pinnedAt).Hours()) / 24
	require.Equal(t, 23, pinnedAgo,
		"v0.18.3 and v0.19.0 were published 20 minutes apart, so this fixture "+
			"cannot distinguish the two clocks on its own — the case that does is "+
			"TestABurstOfReleasesStartsOneClock")
	require.False(t, found.since.IsZero())
}

// A burst of releases in one afternoon must move one clock, not N. Core has
// published 8 and 9 versions in single days, so a gate reading each release as
// its own deadline would redden the fleet on cadence alone — the "trains
// people to ignore red" failure codefly-dev/core#891 warns about.
func TestABurstOfReleasesStartsOneClock(t *testing.T) {
	// v0.19.13 is missing v0.19.14 through v0.19.18: five versions, four of
	// them published within 90 minutes of each other on 2026-10-28.
	found := byFile(t, check(t, "fleet", at))["modules/alpha/services/two/code/go.mod"]

	require.Equal(t, 5, found.behind)
	require.Equal(t, "v0.19.14", found.missing)
	require.Equal(t, 6, int(found.staleFor.Hours())/24,
		"the clock reads the age of the FIRST missing version, so five releases "+
			"in an afternoon are one deadline and not five")
}

// A gate that cannot reach a catalogue must not report green, and must not
// report drift either: it has no verdict, and saying so is the only honest
// answer. Hence exit 2, distinct from exit 1.
func TestNoProxyIsRefusedRatherThanAssumedCurrent(t *testing.T) {
	for _, setting := range []string{"off", "direct", "off,direct"} {
		t.Run(setting, func(t *testing.T) {
			t.Setenv("GOPROXY", setting)
			_, err := proxyBase("")
			require.ErrorIs(t, err, errNoProxy)
		})
	}
}

// GOPROXY is honoured rather than hardcoded, because it is how the consumer's
// own build resolves core. A gate answering from a different source than the
// build is not a verdict on the build.
func TestTheProxyComesFromGOPROXY(t *testing.T) {
	t.Setenv("GOPROXY", "https://corp.example/mod,direct")
	base, err := proxyBase("")
	require.NoError(t, err)
	require.Equal(t, "https://corp.example/mod", base)

	t.Setenv("GOPROXY", "off")
	base, err = proxyBase("https://explicit.example")
	require.NoError(t, err, "an explicit -proxy wins over GOPROXY")
	require.Equal(t, "https://explicit.example", base)

	t.Setenv("GOPROXY", "")
	base, err = proxyBase("")
	require.NoError(t, err)
	require.Equal(t, "https://proxy.golang.org", base)
}

// An annotation is the form the numbers reach the Files view in, and a raw
// newline would end the workflow command and drop everything after it — which
// is how a multi-line failure message becomes a one-word annotation.
func TestAnnotationsEscapeTheirPayloadAndCarryTheFileAndLine(t *testing.T) {
	result := check(t, "fleet", at)
	found := byFile(t, result)["tools/profiles/go.mod"]

	buffer := &bytes.Buffer{}
	annotate(buffer, found)
	line := buffer.String()

	require.True(t, strings.HasPrefix(line, "::error "), "a failure annotates as an error")
	require.Contains(t, line, "file=tools/profiles/go.mod")
	require.Contains(t, line, "line=5", "the annotation lands on the require line")
	require.Contains(t, line, "v0.18.3")
	require.Contains(t, line, "23 days")
	require.Equal(t, 1, strings.Count(line, "\n"), "exactly the one trailing newline")

	require.Equal(t, "a%0Ab%25c", escapeData("a\nb%c"))
	require.Equal(t, "a%3Ab%2Cc", escapeProperty("a:b,c"))
}

// A warning-only report passes. The whole design of the two levels is that a
// repository one week behind is told so without being blocked, so this is the
// property that keeps a core release from reddening the fleet the day it lands.
func TestAWarningOnlyReportPasses(t *testing.T) {
	result := check(t, "untagged-ahead", at)

	require.Equal(t, levelWarn, result.worst())
	require.False(t, result.failed())

	buffer := &bytes.Buffer{}
	render(buffer, result)
	out := buffer.String()
	require.Contains(t, out, "WARNING ONLY — this check passes")
	require.Contains(t, out, "v0.19.18")
}

// The rendered output must state the thresholds in force, so a repository that
// has loosened them cannot look like one holding the fleet standard.
func TestTheRenderedReportStatesTheThresholdsInForce(t *testing.T) {
	buffer := &bytes.Buffer{}
	render(buffer, check(t, "fleet", at))
	out := buffer.String()

	require.Contains(t, out, "core's newest release : v0.19.18")
	require.Contains(t, out, "warn at 3 days behind, fail at 10 days behind")
	require.Contains(t, out, "not on how many")
	require.Contains(t, out, "5 found, 4 pinning core")
	require.Contains(t, out, "What to do")
}

// One instruction per distinct remedy, not one per module: a repository with
// eleven modules on three pins should read three instructions.
func TestRemediesAreGroupedRatherThanRepeated(t *testing.T) {
	groups := check(t, "fleet", at).remedies()

	require.Len(t, groups, 2, "the direct pins share one remedy; the indirect one differs")
	total := 0
	for _, group := range groups {
		total += len(group.files)
	}
	require.Equal(t, 3, total, "the current module needs no remedy")
}

// humanDuration renders in the unit the threshold is stated in, and never
// reports a fresh release as "0 days".
func TestHumanDurationReadsInThresholdUnits(t *testing.T) {
	require.Equal(t, "0 hours", humanDuration(0))
	require.Equal(t, "5 hours", humanDuration(5*time.Hour))
	require.Equal(t, "1 day", humanDuration(25*time.Hour))
	require.Equal(t, "10 days", humanDuration(10*24*time.Hour))
	require.Equal(t, "0 hours", humanDuration(-time.Hour))
}

// The step summary is where the numbers land for a reader who never opens the
// log, so it must carry them and must not be broken by a pipe in a message.
func TestTheStepSummaryCarriesTheNumbers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "summary.md")
	require.NoError(t, writeStepSummary(path, check(t, "fleet", at)))

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	summary := string(raw)

	require.Contains(t, summary, "## core pin drift")
	require.Contains(t, summary, "**v0.19.18**")
	require.Contains(t, summary, "`tools/profiles/go.mod`")
	require.Contains(t, summary, "23 days")
	require.Contains(t, summary, "❌")
	require.Equal(t, `a \| b`, cell("a | b"))
	require.Equal(t, "a b", cell("a\nb"))
}

// A pin to a commit that is not an ancestor of core's default branch fails,
// and it fails BEFORE the merge that would destroy it.
//
// This is obin-ai/module-robin's 2026-11-04 outage, caught one step earlier.
// robin#659 squash-merged and deleted the branch holding a commit its own
// go.mod pinned, and `go mod download` then failed repository-wide with
// "unknown revision". The pin had passed its gate on a reference that merging
// destroyed. A commit sitting on an unmerged branch is that reference, and
// `ahead_by > 0` is how it is recognisable while it still resolves.
//
// The fixture pins codefly-dev/service-go-grpc's real pin,
// v0.19.18-0.20261029150419-9d2348c460ae. That commit is 1 ahead of core's
// main and 6 behind it — a live branch commit, recorded from the API.
func TestAPinToAnUnmergedBranchCommitFails(t *testing.T) {
	result := check(t, "unmerged-branch", at)
	found := result.findings[0]

	require.Equal(t, kindUnreachable, found.kind)
	require.Equal(t, levelFail, found.level)
	require.True(t, result.failed())
	require.Contains(t, found.describe(), "9d2348c460ae")
	require.Contains(t, found.describe(), "NOT an ancestor of core's main")
	require.Contains(t, found.describe(), "1 commit(s) ahead")
	require.Contains(t, found.remedy, "merging cannot destroy")
}

// The module proxy CANNOT answer reachability, which is why the check talks to
// the repository instead. The proxy is a mirror: it caches a version
// permanently and goes on serving it after the commit is gone upstream.
//
// This is not an argument from the documentation — it is the recorded
// behaviour of the two services for one commit. 9d2348c460ae is NOT on core's
// main, and proxy.golang.org serves its pseudo-version with a 200 anyway.
func TestTheProxyResolvesACommitThatIsNotOnTheDefaultBranch(t *testing.T) {
	shared := proxy(t)
	const pseudo = "v0.19.18-0.20261029150419-9d2348c460ae"

	cat, err := newCatalog(context.Background(), shared)
	require.NoError(t, err)
	resolves, err := cat.resolves(context.Background(), pseudo)
	require.NoError(t, err)
	require.True(t, resolves,
		"the proxy resolves this pseudo-version, so a proxy lookup alone would "+
			"have reported this pin healthy")

	reach, err := newReachability(shared, corePath)
	require.NoError(t, err)
	answer := reach.reachableFromDefaultBranch(context.Background(), "9d2348c460ae")
	require.NoError(t, answer.err)
	require.False(t, answer.reachable,
		"while the repository says the commit is not on main -- the two services "+
			"disagree, and only one of them is the one `go mod download` asks when it "+
			"resolves direct")
}

// A reachable commit must NOT be flagged. A check that failed every
// pseudo-version would be indistinguishable from banning them, which is a
// different and much larger decision than the one this gate makes.
func TestAPseudoVersionOnTheDefaultBranchIsNotFlagged(t *testing.T) {
	found := byFile(t, check(t, "fleet", at))["clients/go/go.mod"]

	require.Equal(t, kindUntagged, found.kind, "untagged, not unreachable")
	require.Equal(t, levelWarn, found.level, "a warning for being untagged and behind, not a failure")
	require.Empty(t, found.unverified)
}

// The three answers the comparison can give, read off real recorded responses,
// plus the two it can refuse with.
//
// The post-merge state -- a commit the repository no longer has at all, which
// is where module-robin ended up -- is covered here rather than end to end:
// reproducing it needs a pseudo-version that resolves on the proxy while its
// commit is gone from core, and no such commit exists in core today to record.
// The 404-to-`missing` mapping is the whole of what that state needs.
func TestTheComparisonIsReadFromAheadByAndRefusesWhatItCannotRead(t *testing.T) {
	cases := []struct {
		name      string
		status    int
		body      string
		reachable bool
		aheadBy   int
		missing   bool
		unknown   bool
	}{
		{
			name:      "on the default branch",
			status:    http.StatusOK,
			body:      `{"status":"behind","ahead_by":0,"behind_by":84}`,
			reachable: true,
		},
		{
			name:      "identical to the default branch",
			status:    http.StatusOK,
			body:      `{"status":"identical","ahead_by":0,"behind_by":0}`,
			reachable: true,
		},
		{
			name:    "on an unmerged branch",
			status:  http.StatusOK,
			body:    `{"status":"diverged","ahead_by":2,"behind_by":7}`,
			aheadBy: 2,
		},
		{
			name:    "the repository does not have the commit",
			status:  http.StatusNotFound,
			body:    `{"message":"Not Found"}`,
			missing: true,
		},
		{
			name:    "rate limited",
			status:  http.StatusForbidden,
			body:    `{"message":"API rate limit exceeded"}`,
			unknown: true,
		},
		{
			name:   "a response carrying no counter is unknown, never a pass",
			status: http.StatusOK,
			body:   `{"status":"behind"}`,
			// ahead_by absent: a guard reading a missing field as zero would
			// pass every response shape it does not understand.
			unknown: true,
		},
		{
			name:    "unreadable body",
			status:  http.StatusOK,
			body:    `{{{`,
			unknown: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer server.Close()

			reach, err := newReachability(server.URL, corePath)
			require.NoError(t, err)
			answer := reach.reachableFromDefaultBranch(context.Background(), "deadbeefcafe")

			require.Equal(t, tc.missing, answer.missing)
			if tc.unknown {
				require.ErrorIs(t, answer.err, errReachabilityUnknown)
				require.False(t, answer.reachable, "unknown is never reachable")
				return
			}
			require.NoError(t, answer.err)
			require.Equal(t, tc.reachable, answer.reachable)
			require.Equal(t, tc.aheadBy, answer.aheadBy)
		})
	}
}

// One request per distinct commit, not one per module. module-tracing pins the
// same commit from all four of its modules.
func TestReachabilityIsAskedOncePerCommit(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		_, _ = w.Write([]byte(`{"status":"behind","ahead_by":0}`))
	}))
	defer server.Close()

	reach, err := newReachability(server.URL, corePath)
	require.NoError(t, err)
	for range 4 {
		require.True(t, reach.reachableFromDefaultBranch(context.Background(), "abc123def456").reachable)
	}
	require.Equal(t, 1, requests)
}

// A reachability question that could not be answered must not read as clean.
// Rate limiting is the realistic way this happens, and a gate that passed on it
// would be one an unauthenticated run silently disables.
func TestAnUnansweredReachabilityQuestionIsNeverReportedClean(t *testing.T) {
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/compare/") {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"message":"API rate limit exceeded"}`))
			return
		}
		http.Error(w, "unexpected", http.StatusBadRequest)
	}))
	defer refusing.Close()

	result, err := run(context.Background(), options{
		root:       filepath.Join("testdata", "repos", "unmerged-branch"),
		configPath: defaultConfigPath,
		proxy:      proxy(t),
		api:        refusing.URL,
		now:        at,
	})
	require.NoError(t, err, "a rate limit must not wedge the gate")

	found := result.findings[0]
	require.NotEmpty(t, found.unverified, "the finding records that the question went unanswered")
	require.Contains(t, found.unverified, "NOT verified")
	require.Contains(t, found.describe(), "CAUTION")
	require.NotEqual(t, levelOK, found.level, "and is never clean")
	require.Equal(t, kindUntagged, found.kind,
		"it stays an untagged pin: an unanswered question is not evidence of a "+
			"destroyed reference either")
}

// The module path decides which repository is asked, rather than it being
// written down twice.
func TestTheRepositoryComesFromTheModulePath(t *testing.T) {
	owner, repo, ok := githubRepoOf("github.com/codefly-dev/core")
	require.True(t, ok)
	require.Equal(t, "codefly-dev", owner)
	require.Equal(t, "core", repo)

	_, _, ok = githubRepoOf("golang.org/x/mod")
	require.False(t, ok, "a non-github module has no commits to ask about")

	_, err := newReachability("", "golang.org/x/mod")
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a github.com module path")
}

// A pseudo-version whose commit cannot be read must not be sent to the API as
// one: the API would 404, and a 404 means "core does not have this commit",
// which would be a FALSE orphan failure caused by the gate rather than the pin.
func TestAnUnreadableCommitIsUnverifiedRatherThanAFalseOrphan(t *testing.T) {
	require.Equal(t, "(unknown)", shortRev("v0.19.18"),
		"a plain version carries no commit, and shortRev says so rather than "+
			"returning something URL-shaped")

	// The judged path never reaches the API with a rev it could not read: it
	// records the question as unanswered instead.
	found := byFile(t, check(t, "fleet", at))["clients/go/go.mod"]
	require.Empty(t, found.unverified, "this one's rev reads fine")
	require.Equal(t, kindUntagged, found.kind)
}
