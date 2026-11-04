// Command corepindrift reports how far behind core a repository's pins are,
// and fails when one has been behind for too long.
//
//	go run ./internal/corepindrift <repository-root>
//
// Core owns the Work Context — the signed capability every v2 service verifies
// on every call. A verification rule tightened here becomes true in a consumer
// only when that consumer bumps its pin, so the honest statement of the
// platform's posture is never "the verifier requires X"; it is "the verifier
// requires X wherever somebody happened to bump". Nothing reported which
// repositories those were (codefly-dev/core#891).
//
// This is the thing that reports it. It runs in the CONSUMER's repository,
// against the consumer's go.mod files, through
// .github/workflows/core-pin-drift.yml. See docs/core-pin-drift.md for the
// thresholds and the argument for them.
//
// Two decisions are worth knowing before reading the code:
//
// THE AUTHORITY IS THE MODULE PROXY, NOT GITHUB RELEASES. Core has 460 version
// tags and 62 GitHub Releases — releases were only ever created for the last
// six weeks of them. A gate counting GitHub Releases is therefore wrong about
// 398 of the versions a consumer can actually pin, and says nothing at all
// about a pin like v0.15.1. GOPROXY's @v/list is exactly "what a go.mod may
// resolve", which is the question a pin asks, and it is the same source the
// consumer's own build resolves core from. core/releaseupdate was considered
// and rejected for this: it discovers GitHub Releases in order to select a
// downloadable binary asset for self-update, which is a different question
// with a different answer set.
//
// THE CLOCK IS ON THE OLDEST MISSING VERSION, not on the pin. "Pinned to
// something 6 days old" is not the risk; "a newer version has been available
// for 6 days and this repository has not taken it" is. So drift is measured as
// the age of the oldest version this repository is missing, which has three
// properties worth having: every core release starts one clock for the whole
// fleet rather than N clocks; a burst of releases in one afternoon moves
// nobody's clock (the oldest missing version is still that afternoon's first);
// and bumping to something that is not the newest does NOT reset it, because
// the version it skipped is still the oldest one missing.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"
	"time"
)

// corePath is the module every consumer in the fleet pins, and the only one
// this command has an opinion about.
const corePath = "github.com/codefly-dev/core"

// The fleet thresholds. These are core's numbers, not a per-repository knob:
// the point of codefly-dev/core#891 is one threshold written down once, so the
// reusable workflow exposes no inputs and a repository that wants different
// numbers is having a conversation rather than editing a YAML value.
//
// The argument for each, in full, is in docs/core-pin-drift.md. In short:
//
//   - warnAfter = 3 days. Long enough that a release lands without reddening
//     anything, short enough to be visible within the week.
//   - failAfter = 10 days. codefly-dev/core#891 names two weeks unconsumed as
//     the case that actually matters, so the gate has to fire before two
//     weeks; and 10 days is longer than any single bump cycle, so an ordinary
//     cadence never sees red.
//
// THERE IS NO THRESHOLD ON THE NUMBER OF VERSIONS BEHIND, and that is a
// finding rather than an omission. "More than N releases behind" is the
// obvious shape and it does not survive core's cadence: the densest ten-day
// window in core's history published 63 versions (2026-08-05 to 2026-08-15)
// and the densest three-day window published 30. So any count low enough to
// fire is also low enough to fire on a repository that is three days behind
// during an ordinary burst — which is the "trains people to ignore red"
// failure codefly-dev/core#891 warns about — and any count high enough to
// clear a burst is one the 10-day rule has already failed long before. A
// threshold that can never be the deciding axis is a decoration.
//
// The count is still REPORTED on every finding, because "eleven versions
// behind" is what makes a reader act. It is a number, not a gate.
const (
	warnAfter = 3 * 24 * time.Hour
	failAfter = 10 * 24 * time.Hour
)

func main() {
	configPath := flag.String("config", defaultConfigPath,
		"repository-relative path to the exemption file")
	proxy := flag.String("proxy", "",
		"module proxy base URL; defaults to the first usable GOPROXY entry")
	api := flag.String("api", "",
		"GitHub API base URL, used to check that a pinned commit is reachable from "+
			strconv.Quote(defaultBranch)+"; defaults to https://api.github.com")
	nowText := flag.String("now", "",
		"RFC3339 instant to measure ages against; defaults to the current time")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(),
			"usage: go run ./internal/corepindrift [flags] <repository-root>\n\n"+
				"Reports how far behind %s every go.mod in <repository-root> is pinned.\n"+
				"See docs/core-pin-drift.md.\n\nflags:\n", corePath)
		flag.PrintDefaults()
	}
	flag.Parse()

	if flag.NArg() != 1 {
		flag.Usage()
		os.Exit(2)
	}

	now := time.Now().UTC()
	if *nowText != "" {
		parsed, err := time.Parse(time.RFC3339, *nowText)
		if err != nil {
			fail(fmt.Errorf("parse -now: %w", err))
		}
		now = parsed.UTC()
	}

	report, err := run(context.Background(), options{
		root:       flag.Arg(0),
		configPath: *configPath,
		proxy:      *proxy,
		api:        *api,
		now:        now,
	})
	if err != nil {
		fail(err)
	}

	render(os.Stdout, report)
	if summary := os.Getenv("GITHUB_STEP_SUMMARY"); summary != "" {
		if err := writeStepSummary(summary, report); err != nil {
			// A summary that cannot be written is not a reason to lose the
			// verdict, which has already been printed to the log.
			fmt.Fprintf(os.Stderr, "note: could not write the step summary: %v\n", err)
		}
	}
	if report.failed() {
		os.Exit(1)
	}
}

// fail reports a refusal the gate could not get past. It exits 2 rather than
// 1, so "this repository is behind" and "this gate could not answer" are
// distinguishable: a gate that cannot reach the proxy must not read as green,
// and must not read as drift either.
func fail(err error) {
	fmt.Fprintf(os.Stderr, "::error::core-pin-drift could not answer: %v\n", err)
	os.Exit(2)
}

// options is one invocation.
type options struct {
	root       string
	configPath string
	proxy      string
	api        string
	now        time.Time
}

// run does the whole job: find the modules, learn what core has published,
// judge each pin.
func run(ctx context.Context, opts options) (report, error) {
	modules, err := discover(opts.root)
	if err != nil {
		return report{}, err
	}

	config, err := loadConfig(opts.root, opts.configPath)
	if err != nil {
		return report{}, err
	}

	pinned := make([]goMod, 0, len(modules))
	for _, found := range modules {
		if found.pin != "" || found.replacedLocally {
			pinned = append(pinned, found)
		}
	}
	if len(pinned) == 0 {
		return report{}, fmt.Errorf(
			"no go.mod under %s requires %s.\n"+
				"Either this repository does not pin core — in which case it should not "+
				"call this gate — or the checkout is empty. Refusing to report green on "+
				"a repository whose pins were never found",
			opts.root, corePath)
	}

	catalog, err := newCatalog(ctx, opts.proxy)
	if err != nil {
		return report{}, err
	}

	reach, err := newReachability(opts.api, corePath)
	if err != nil {
		return report{}, err
	}

	result := report{
		latest:    catalog.latest(),
		now:       opts.now,
		warnAfter: warnAfter,
		failAfter: failAfter,
		scanned:   len(modules),
	}
	for _, found := range pinned {
		judged, err := judge(ctx, found, config, catalog, reach, opts.now)
		if err != nil {
			return report{}, err
		}
		result.findings = append(result.findings, judged)
	}

	// A stale exemption is a hole: it names a module that no longer exists, so
	// nobody notices that the reason it recorded stopped applying. Fail closed.
	if unused := config.unused(); len(unused) > 0 {
		return report{}, fmt.Errorf(
			"%s exempts %d path(s) that match no go.mod requiring %s: %v.\n"+
				"An exemption that matches nothing is a recorded reason nobody is "+
				"holding to anything. Remove it, or correct the path",
			opts.configPath, len(unused), corePath, unused)
	}

	return result, nil
}

var errNoProxy = errors.New(
	"GOPROXY is off or direct-only, so there is no catalogue of core's published " +
		"versions to compare against. This gate needs a module proxy; set GOPROXY " +
		"to one, or pass -proxy")
