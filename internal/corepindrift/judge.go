package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/mod/module"
	"golang.org/x/mod/semver"
)

// level is how loudly a finding reports.
type level int

const (
	levelOK level = iota
	levelWarn
	levelFail
)

func (l level) String() string {
	switch l {
	case levelWarn:
		return "WARN"
	case levelFail:
		return "FAIL"
	default:
		return "ok"
	}
}

// kind is what shape a pin turned out to be. Each one fails differently, which
// is the point: "eleven releases behind", "pinned to an untagged commit" and
// "pinned to a version core never published" need different remedies, and a
// gate that renders them all as "out of date" sends the reader to the wrong
// fix.
type kind string

const (
	// kindCurrent — the pin is core's newest stable version.
	kindCurrent kind = "current"
	// kindBehind — the pin is a published version with newer ones above it.
	kindBehind kind = "behind"
	// kindUntagged — a pseudo-version: an untagged commit on core's main.
	kindUntagged kind = "untagged"
	// kindOrphaned — a pseudo-version naming a commit the repository no longer
	// has at all.
	kindOrphaned kind = "orphaned"
	// kindUnreachable — a pseudo-version naming a commit that exists but is
	// not an ancestor of core's default branch: a branch commit, which a
	// squash-merge of that branch will destroy.
	kindUnreachable kind = "unreachable"
	// kindUnpublished — a plain version core has never published.
	kindUnpublished kind = "unpublished"
	// kindLocalReplace — a `replace` to a filesystem path.
	kindLocalReplace kind = "local-replace"
	// kindExempt — the repository declared this module out of scope.
	kindExempt kind = "exempt"
)

// finding is one module's verdict.
type finding struct {
	goMod goMod
	kind  kind
	level level

	// pin is the version judged, after any replacement.
	pin string
	// behind is how many stable versions sit above the pin.
	behind int
	// missing is the oldest stable version the pin does not have, "" when none.
	missing string
	// since is when `missing` was published — the instant the clock started.
	since time.Time
	// staleFor is how long `missing` has been available.
	staleFor time.Duration
	// pinnedAt is when the pinned version itself was published, for the
	// report. Reported, never judged: the pin's own age is not the risk.
	pinnedAt time.Time
	// reason is the exemption's recorded reason, for kindExempt.
	reason string
	// detail is the one sentence explaining this kind for this module.
	detail string
	// remedy is what the reader should change.
	remedy string
	// degraded is set when the go.mod could only be read loosely, so a
	// `replace` in it was not seen and this verdict may be about the wrong
	// core.
	degraded error
	// unverified is set when a question this finding depends on could not be
	// answered — today, only commit reachability. A finding carrying one is
	// never reported clean.
	unverified string
}

// judge decides one module's verdict.
//
// The order matters. An exemption is read first so an exempt module costs no
// requests and no opinion. A local replacement is read next, because when one
// is present the `require` line is not what builds and judging it would be
// judging the wrong thing.
func judge(ctx context.Context, found goMod, cfg *config, cat *catalog, reach *reachability, now time.Time) (finding, error) {
	result, err := judgePin(ctx, found, cfg, cat, reach, now)
	if err != nil {
		return finding{}, err
	}
	// A verdict drawn from a file that could not be read in full is never
	// clean, whatever the version numbers said: the replace directive this
	// gate most needs to see is the one it could not read.
	if result.level == levelOK && result.kind != kindExempt &&
		(result.degraded != nil || result.unverified != "") {
		result.level = levelWarn
	}
	return result, nil
}

func judgePin(ctx context.Context, found goMod, cfg *config, cat *catalog, reach *reachability, now time.Time) (finding, error) {
	latest := cat.latest()
	result := finding{goMod: found, pin: found.effectivePin()}

	if entry, ok := cfg.exempt(found.dir); ok {
		result.kind = kindExempt
		result.level = levelOK
		result.reason = entry.Reason
		result.detail = fmt.Sprintf("exempt: %s", entry.Reason)
		return result, nil
	}

	if found.replacedLocally {
		result.kind = kindLocalReplace
		result.level = levelFail
		result.detail = fmt.Sprintf(
			"a replace directive redirects %s to %s, a filesystem path, so the "+
				"version in `require` is not the core this module builds against and "+
				"this gate cannot answer from it",
			corePath, found.replacementTarget)
		result.remedy = fmt.Sprintf(
			"remove the replace directive and pin a published version (%s is the "+
				"newest). A local replace is a development convenience; committed, it "+
				"means the build resolves core from a path that exists on one machine. "+
				"`codefly agent deps --unlink` is the verb that undoes a linked core",
			latest)
		return result, nil
	}

	if found.replacement != "" {
		result.detail = fmt.Sprintf("replaced to %s; judged on that version. ", found.replacementTarget)
	}

	// A go.mod that only parsed loosely had its `replace` directives skipped,
	// so this verdict is about the `require` line and may be about the wrong
	// core. Reported as a warning rather than passed: unreadable is not clean.
	if found.parseDegraded != nil {
		result.degraded = found.parseDegraded
	}

	if module.IsPseudoVersion(result.pin) {
		return judgePseudo(ctx, result, cat, reach, now, latest)
	}

	if !semver.IsValid(result.pin) {
		return finding{}, fmt.Errorf("%s: %q is not a valid semantic version", found.file, result.pin)
	}

	// A plain version that the proxy does not list is one core never
	// published. This is NOT the same as "ahead", and reading it as ahead is
	// the specific accident codefly-dev/core#891 asks about: at the time of
	// writing nothing resolved v0.19.19, so a checker comparing numbers would
	// have called that pin up to date — in fact newer than core's newest —
	// while `go mod download` could not fetch it at all.
	resolves, err := cat.resolves(ctx, result.pin)
	if err != nil {
		return finding{}, fmt.Errorf("%s: resolve %s: %w", found.file, result.pin, err)
	}
	if !resolves {
		result.kind = kindUnpublished
		result.level = levelFail
		ahead := semver.Compare(result.pin, latest) > 0
		position := "below"
		if ahead {
			position = "above"
		}
		result.detail += fmt.Sprintf(
			"%s is not a version core has published — the module proxy does not "+
				"list it and cannot resolve it. It sorts %s core's newest release %s, "+
				"so comparing version numbers alone would have read it as %s",
			result.pin, position, latest,
			map[bool]string{true: "ahead of core and therefore fine", false: "merely behind"}[ahead])
		result.remedy = fmt.Sprintf(
			"pin a version that exists. `codefly agent deps --pin %s` sets it, and "+
				"a pin nothing can resolve fails every fresh `go mod download` "+
				"regardless of this gate", latest)
		return result, nil
	}

	newer := cat.newerThan(result.pin)
	result.behind = len(newer)
	if published, err := cat.publishedAt(ctx, result.pin); err == nil {
		result.pinnedAt = published
	} else if !errors.Is(err, errNotFound) {
		return finding{}, fmt.Errorf("%s: %w", found.file, err)
	}

	if len(newer) == 0 {
		result.kind = kindCurrent
		result.level = levelOK
		result.detail += fmt.Sprintf("pinned to %s, core's newest release", result.pin)
		return result, nil
	}

	result.kind = kindBehind
	missing, since, err := cat.oldestDescribable(ctx, newer)
	if err != nil {
		return finding{}, fmt.Errorf("%s: %w", found.file, err)
	}
	result.missing = missing
	result.since = since
	result.staleFor = now.Sub(since)
	result.level = levelFor(result.staleFor)
	result.detail += fmt.Sprintf(
		"pinned to %s; core has published %d version(s) since, the oldest of "+
			"which (%s) has been available for %s",
		result.pin, result.behind, result.missing, humanDuration(result.staleFor))
	result.remedy = remedyFor(found, latest)
	return result, nil
}

// judgePseudo handles a pin to an untagged commit.
//
// 26 of the 42 go.mod files pinning core across the nine repositories in
// codefly-dev/core#891 are pseudo-versions, so this is the fleet's commonest
// pin shape and not an edge case. It gets its own verdict for two reasons that
// a version comparison cannot express:
//
//   - A pseudo-version sorts BELOW its base version (it is a prerelease of
//     it), so `v0.19.19-0.20261104012958-be256d5c567d` is above v0.19.18 and
//     below v0.19.19. The ordering is right, but "0 releases behind" would be
//     the whole report, and the interesting fact — this is not a release — is
//     exactly what gets lost.
//   - Core squash-merges. The commit a pseudo-version names can stop being
//     reachable from main, and then the pin resolves for nobody. That is a
//     hard failure whatever the dates say.
func judgePseudo(ctx context.Context, result finding, cat *catalog, reach *reachability, now time.Time, latest string) (finding, error) {
	commitTime, err := module.PseudoVersionTime(result.pin)
	if err != nil {
		return finding{}, fmt.Errorf("%s: read the commit time out of %s: %w",
			result.goMod.file, result.pin, err)
	}
	result.pinnedAt = commitTime.UTC()

	resolves, err := cat.resolves(ctx, result.pin)
	if err != nil {
		return finding{}, fmt.Errorf("%s: resolve %s: %w", result.goMod.file, result.pin, err)
	}
	if !resolves {
		result.kind = kindOrphaned
		result.level = levelFail
		result.detail += fmt.Sprintf(
			"%s names commit %s, which the module proxy cannot resolve. Core "+
				"squash-merges, so a commit reachable from a branch when this was "+
				"pinned is not reachable from main afterwards — this pin fetches for "+
				"nobody, including a fresh CI checkout",
			result.pin, shortRev(result.pin))
		result.remedy = fmt.Sprintf(
			"pin a published version: `codefly agent deps --pin %s`", latest)
		return result, nil
	}

	// Resolving on the proxy is NOT evidence the commit still exists: the proxy
	// is a mirror and caches a version permanently, so it keeps serving a
	// pseudo-version after the commit is gone upstream. Ask the repository.
	//
	// This is what obin-ai/module-robin needed on 2026-11-04: robin#659
	// squash-merged, deleted the branch holding a commit its go.mod pinned, and
	// every Go job went red with "unknown revision". The pin had passed its
	// gate on a reference that merging destroyed.
	// A rev this cannot read must not be sent as one: the API would 404 on it
	// and the 404 means "core does not have this commit", which would be a
	// false orphan failure attributable to the gate rather than the pin.
	rev, revErr := module.PseudoVersionRev(result.pin)
	switch {
	case reach == nil:
	case revErr != nil || rev == "":
		result.unverified = unverifiedFor(fmt.Errorf(
			"%w: no commit could be read out of %s", errReachabilityUnknown, result.pin))
	default:
		answer := reach.reachableFromDefaultBranch(ctx, rev)
		switch {
		case answer.missing:
			result.kind = kindOrphaned
			result.level = levelFail
			result.detail += fmt.Sprintf(
				"%s names commit %s, which core no longer has. This is the state a "+
					"squash-merge leaves behind when it deletes the branch a pin named: "+
					"the module proxy still serves the version from its cache, and every "+
					"resolution that goes direct fails with \"unknown revision\"",
				result.pin, shortRev(result.pin))
			result.remedy = fmt.Sprintf(
				"pin a published version: `codefly agent deps --pin %s`", latest)
			return result, nil
		case answer.err != nil:
			result.unverified = unverifiedFor(answer.err)
		case !answer.reachable:
			result.kind = kindUnreachable
			result.level = levelFail
			result.detail += fmt.Sprintf(
				"%s names commit %s, which exists but is NOT an ancestor of core's %s "+
					"— it is %d commit(s) ahead of it, so it sits on a branch. Core "+
					"squash-merges: when that branch merges, this exact commit stops "+
					"existing and this pin fails to fetch everywhere at once. That is not "+
					"hypothetical — it is what took obin-ai/module-robin's main red on "+
					"2026-11-04",
				result.pin, shortRev(result.pin), defaultBranch, answer.aheadBy)
			result.remedy = fmt.Sprintf(
				"pin something merging cannot destroy: a release (`codefly agent deps "+
					"--pin %s`), or a pseudo-version of a commit that is already on %s. "+
					"A pin to an unmerged branch commit is a reference with an expiry "+
					"date nobody is tracking", latest, defaultBranch)
			return result, nil
		}
	}

	newer := cat.newerThan(result.pin)
	result.behind = len(newer)
	result.kind = kindUntagged

	if len(newer) == 0 {
		// Ahead of every release: core has tagged nothing since this commit.
		// A warning rather than a pass, because the pin is still not a release
		// — nothing states what is in it, and it is one squash away from the
		// orphaned case above.
		result.level = levelWarn
		result.detail += fmt.Sprintf(
			"%s is a pseudo-version: commit %s of core's main from %s, which is "+
				"ahead of core's newest release %s. Nothing is behind, but nothing "+
				"states what this pin contains either, and core squash-merges",
			result.pin, shortRev(result.pin), result.pinnedAt.Format(time.DateOnly), latest)
		result.remedy = fmt.Sprintf(
			"move to a release once one contains this commit. Until then this is a "+
				"pin to unreleased core, which `codefly agent release --pin` is the "+
				"verb for closing (core's newest release is %s)", latest)
		return result, nil
	}

	missing, since, err := cat.oldestDescribable(ctx, newer)
	if err != nil {
		return finding{}, fmt.Errorf("%s: %w", result.goMod.file, err)
	}
	result.missing = missing
	result.since = since
	result.staleFor = now.Sub(since)
	// At least a warning: an untagged pin that is also behind is never clean.
	result.level = levelFor(result.staleFor)
	if result.level == levelOK {
		result.level = levelWarn
	}
	result.detail += fmt.Sprintf(
		"%s is a pseudo-version: commit %s of core's main from %s, not a release. "+
			"Core has published %d version(s) above it, the oldest of which (%s) has "+
			"been available for %s",
		result.pin, shortRev(result.pin), result.pinnedAt.Format(time.DateOnly),
		result.behind, result.missing, humanDuration(result.staleFor))
	result.remedy = remedyFor(result.goMod, latest)
	return result, nil
}

// levelFor applies the thresholds, which are on AGE alone.
//
// The number of versions behind is reported and never judged: on core's
// cadence no count can be both low enough to mean something and high enough
// to survive a release burst. The constants in main.go carry the measurement;
// docs/core-pin-drift.md carries the argument.
func levelFor(staleFor time.Duration) level {
	if staleFor >= failAfter {
		return levelFail
	}
	if staleFor >= warnAfter {
		return levelWarn
	}
	return levelOK
}

// remedyFor names the command that owns closing the gap. The CLI owns the
// fleet verbs — this gate reports drift, it does not close it — so the remedy
// is a `codefly` verb and not a `go get`, which reaches go.mod and leaves the
// nested base fixtures and factory templates behind.
func remedyFor(found goMod, latest string) string {
	// A fixture pinned to an old core is usually meant to be pinned to an old
	// core: codefly-dev/cli has two, and bumping one breaks the test that
	// asserts against its contents. Telling the reader to bump it would be
	// telling them to break their suite, so the exemption is named first for
	// these — declared by name, with a reason, rather than skipped by a
	// convention this gate applied silently.
	if isFixturePath(found.dir) {
		return fmt.Sprintf(
			"this looks like test data. If it is pinned to an old core deliberately, "+
				"declare it in %s with a reason — bumping a fixture breaks the test "+
				"that asserts against its contents. If it is not deliberate, "+
				"`codefly agent deps --pin %s`", defaultConfigPath, latest)
	}
	if found.indirect {
		return fmt.Sprintf(
			"core is an INDIRECT requirement here, so bumping it directly is not the "+
				"fix: raise the dependency that pulls it in, then `go mod tidy`. "+
				"Minimum version selection takes the maximum across the graph, so this "+
				"pin still decides which core compiles in. Core's newest release is %s",
			latest)
	}
	return fmt.Sprintf(
		"`codefly agent deps --pin %s` from the repository root updates every lock "+
			"this module owns, then tidies and verifies the build. It is the verb that "+
			"owns the pin; a hand edit to go.mod reaches this file and leaves the "+
			"nested fixtures and templates behind", latest)
}

// shortRev is the 12-character commit a pseudo-version names.
func shortRev(version string) string {
	rev, err := module.PseudoVersionRev(version)
	if err != nil || rev == "" {
		return "(unknown)"
	}
	return rev
}

// humanDuration renders an age the way the failure message needs to read: in
// days, because the threshold is in days, with hours below one day so a fresh
// release does not report "0 days".
func humanDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%d hours", int(d.Hours()))
	}
	days := int(d.Hours()) / 24
	if days == 1 {
		return "1 day"
	}
	return fmt.Sprintf("%d days", days)
}
