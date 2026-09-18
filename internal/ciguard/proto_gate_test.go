package ciguard

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// The proto breaking-change gate in go.yml runs `buf breaking` against main.
// Which rules it applies, and which buf runs them, are configuration no other
// test reads — and both were wrong when the gate first landed.

// proto/buf.yaml must select PACKAGE. With no `breaking:` block at all — how
// the gate shipped — buf falls back to its FILE default, whose per-file
// deletion rules (MESSAGE_NO_DELETE, ENUM_NO_DELETE) reject moving a message
// to another file in the same package. Consumers reach these schemas over the
// wire or through the generated Go package, and neither can observe which
// .proto file a message is declared in, so FILE blocks refactors that break
// nothing: relocating enum StorageAuthorityKind to a new file builds clean yet
// exits 100 with "was deleted from file".
//
// PACKAGE still blocks what matters — a renamed enum value, a changed field
// type, and a message deleted from the package all exit 100 under it.
func TestProtoModuleUsesPackageBreakingRules(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "proto", "buf.yaml"))
	require.NoError(t, err)

	var cfg struct {
		Breaking struct {
			Use []string `yaml:"use"`
		} `yaml:"breaking"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &cfg))

	require.Equal(t, []string{"PACKAGE"}, cfg.Breaking.Use,
		"proto/buf.yaml must select the PACKAGE breaking rules. Absent, buf applies "+
			"its FILE default, which fails a message moved between files in the same "+
			"package even though the wire and the generated Go symbol are identical.")
}

var bakedBufPin = regexp.MustCompile(`github\.com/bufbuild/buf/cmd/buf@v([0-9]+\.[0-9]+\.[0-9]+)`)

var makefileBufPin = regexp.MustCompile(`(?m)^BUF_VERSION\s*[:?]?=\s*([0-9]+\.[0-9]+\.[0-9]+)`)

// Every buf the Makefile runs has to reach its version through $(BUF_VERSION).
// A literal version in a recipe is a buf this guard cannot see: BUF_VERSION
// stays equal to the Dockerfile, the test stays green, and the targets run
// something else entirely.
var makefileBufRun = regexp.MustCompile(`github\.com/bufbuild/buf/cmd/buf@v\$\(BUF_VERSION\)`)

// CI's buf, the buf the proto companion bakes, and the buf the Makefile targets
// run must all be the same one. The companion generates every consumer's
// bindings; CI decides whether a schema change is allowed to land; the Makefile
// is what a developer runs before pushing. Run them on different versions and
// the gate can pass a schema the generator then chokes on, or a local check can
// report clean on a buf that is not the one judging the change.
//
// Nothing keeps these together on its own: buf-setup-action's `version` input
// defaults to the action's own release rather than the latest, and Dependabot
// moves the SHA pin above it but never the input — so the version means
// whatever it meant the day someone typed it. CI shipped 1.73.0 against the
// companion's 1.71.0 for exactly that reason, and the Makefile pin joined this
// check because the docs used to prescribe a bare `buf`, which answered from
// whatever was on PATH.
func TestWorkflowBufMatchesTheCompanionImage(t *testing.T) {
	root := repoRoot(t)

	dockerfile, err := os.ReadFile(filepath.Join(root, "companions", "proto", "Dockerfile"))
	require.NoError(t, err)
	baked := bakedBufPin.FindSubmatch(dockerfile)
	require.NotNil(t, baked,
		"companions/proto/Dockerfile no longer pins github.com/bufbuild/buf/cmd/buf "+
			"to an explicit version, so there is nothing for CI to match")
	want := string(baked[1])

	makefile, err := os.ReadFile(filepath.Join(root, "Makefile"))
	require.NoError(t, err)

	// Exactly one declaration: make takes the last assignment, this regex takes
	// the first, so a second one is a pin that reads equal here and runs
	// different.
	pins := makefileBufPin.FindAllSubmatch(makefile, -1)
	require.Len(t, pins, 1,
		"the Makefile must declare BUF_VERSION exactly once (found %d), or `make "+
			"buf-lint`, `make buf-breaking` and `make buf-install` cannot be pinned to "+
			"the buf CI and the companion run. make uses the last assignment; this "+
			"guard would read the first.",
		len(pins))
	require.Equal(t, want, string(pins[0][1]),
		"the Makefile pins buf %s but companions/proto/Dockerfile bakes %s. A local "+
			"`make buf-breaking` would then answer from a different buf than the gate "+
			"that decides whether the schema lands.",
		string(pins[0][1]), want)

	// The pin has to be what the recipes actually run. Checking the declaration
	// alone leaves the hole open: `BUF := go run .../buf@v1.73.0` beside
	// `BUF_VERSION := 1.71.0` passes every assertion above while both targets
	// execute 1.73.0 against the companion's 1.71.0 — the exact pair this test's
	// comment records as the original bug.
	require.Nil(t, bakedBufPin.Find(makefile),
		"the Makefile invokes buf at a literal version (%q). Every invocation must "+
			"go through @v$(BUF_VERSION), or the version the targets run is one this "+
			"guard cannot see and BUF_VERSION becomes decoration.",
		string(bakedBufPin.Find(makefile)))
	require.NotNil(t, makefileBufRun.Find(makefile),
		"no Makefile recipe runs github.com/bufbuild/buf/cmd/buf@v$(BUF_VERSION), so "+
			"BUF_VERSION pins nothing that executes")

	installed := 0
	for _, path := range workflowFiles(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)

		var wf workflow
		require.NoError(t, yaml.Unmarshal(raw, &wf), path)

		for jobName, job := range wf.Jobs {
			for _, step := range job.Steps {
				if !strings.Contains(step.Uses, "bufbuild/buf-setup-action") {
					continue
				}
				version, ok := step.With["version"]
				require.True(t, ok,
					"%s: job %q installs buf without pinning `version`; the action then "+
						"installs its own default release, which is not the latest and not "+
						"what the companion bakes.",
					filepath.Base(path), jobName)
				installed++
				require.Equal(t, want, fmt.Sprint(version),
					"%s: job %q installs buf %v but companions/proto/Dockerfile bakes %s. "+
						"The gate and the generator have to run the same buf, or CI can pass "+
						"a schema the companion then fails to generate.",
					filepath.Base(path), jobName, version, want)
			}
		}
	}
	require.NotZero(t, installed,
		"no workflow installs buf, so the proto breaking-change gate cannot run")
}

var makefileBreakingAgainst = regexp.MustCompile(`(?m)^.*breaking --against.*$`)

// The local breaking check has to take the MERGE BASE as its baseline, not the
// tip of main. CI checks out the pull request already merged into main, so a
// package main gained after the branch was cut sits on both sides of its
// comparison. Point the local check at main's tip instead and that same package
// is missing from the branch alone, so buf calls it a deletion: `make
// buf-breaking` shipped exiting 100 with `Previously present package
// "codefly.runnable.receipts.v0" was deleted` on a branch that changed no
// .proto at all, while CI on the same commit was green.
//
// That is the failure this whole file exists to prevent, one axis over: a local
// check whose verdict is not the gate's verdict. It is worse than an unpinned
// buf, because a check that cries wolf is one developers stop running.
func TestLocalBreakingCheckBaselineIsTheMergeBase(t *testing.T) {
	makefile, err := os.ReadFile(filepath.Join(repoRoot(t), "Makefile"))
	require.NoError(t, err)

	invocation := makefileBreakingAgainst.Find(makefile)
	require.NotNil(t, invocation,
		"the Makefile no longer runs `buf breaking --against`, so there is no local "+
			"equivalent of the gate for a developer to run before pushing")

	require.Contains(t, string(invocation), "git merge-base",
		"the local breaking check takes its baseline from %q. CI compares the pull "+
			"request MERGED INTO main against main, so a package main gained since this "+
			"branch was cut is present on both of its sides; compared against main's tip "+
			"the same package is missing here only and buf reports it as a deletion. Use "+
			"$(git merge-base <remote>/main HEAD).",
		strings.TrimSpace(string(invocation)))
}

const ciTableHeading = "CI additionally runs, and all of these are runnable locally:"

// A row under that heading is read as a gate that catches the mistake before
// merge. `buf lint` is in no workflow — only `buf breaking` is — so naming it
// there promises a gate that does not exist, and the 363 unenforced COMMENTS
// findings standing in proto/ are what that promise has already cost.
//
// Conditional, not a ban: wire `buf lint` into a workflow and this test stops
// objecting, because the row will have become true.
func TestCITableDoesNotPromiseAGateThatDoesNotRun(t *testing.T) {
	var workflows strings.Builder
	for _, path := range workflowFiles(t) {
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		workflows.Write(raw)
	}
	if strings.Contains(workflows.String(), "buf lint") {
		return
	}

	for _, row := range ciTableRows(t) {
		require.NotContains(t, row, "buf lint",
			"%s lists `buf lint` under %q, but no workflow runs it — only `buf "+
				"breaking` does. Move it to the local-only guidance below the table.",
			agentContextFile, ciTableHeading)
		require.NotContains(t, row, "buf-lint",
			"%s lists `make buf-lint` under %q, but no workflow runs buf lint — only "+
				"`buf breaking` is in CI. Move it to the local-only guidance below the "+
				"table.",
			agentContextFile, ciTableHeading)
	}
}

// ciTableRows returns the table rows following the "CI additionally runs"
// heading in AGENTS.md.
func ciTableRows(t *testing.T) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(repoRoot(t), agentContextFile))
	require.NoError(t, err)

	lines := strings.Split(string(raw), "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, ciTableHeading) {
			start = i
			break
		}
	}
	require.NotEqual(t, -1, start,
		"%s no longer contains %q, so the rows claiming to be CI steps cannot be "+
			"located. Update ciTableHeading to whatever the heading became.",
		agentContextFile, ciTableHeading)

	var rows []string
	for _, line := range lines[start:] {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "|") {
			rows = append(rows, trimmed)
			continue
		}
		if len(rows) > 0 {
			break
		}
	}
	require.NotEmpty(t, rows,
		"%s: the %q table has no rows", agentContextFile, ciTableHeading)
	return rows
}
