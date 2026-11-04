# The core-pin drift gate

Core owns the Work Context — the signed capability every v2 service verifies on
every call. A verification rule tightened in core becomes true in a consumer
only when that consumer bumps its pin. So the honest statement of the
platform's posture was never "the verifier requires a sound issued-at"; it was
"the verifier requires a sound issued-at in whichever repositories happen to
have bumped", and no artifact anywhere said which those were
([#891](https://github.com/codefly-dev/core/issues/891)).

This is the thing that says which those are. Core ships the checker
([`internal/corepindrift`](../internal/corepindrift)) and a reusable workflow
([`.github/workflows/core-pin-drift.yml`](../.github/workflows/core-pin-drift.yml));
a consumer adopts it in one line.

## Adopting it

Add this file to the consuming repository, verbatim:

```yaml
# .github/workflows/core-pin-drift.yml
name: core-pin-drift

on:
  pull_request:
  push:
    branches: [main]

jobs:
  core-pin-drift:
    uses: codefly-dev/core/.github/workflows/core-pin-drift.yml@main
```

Then require the status check. **The context name is**

```
core-pin-drift / drift
```

An administrator requires it on `main` in repository settings — rulesets, under
`required_status_checks`. Until that is done the gate is a decoration: several
findings in this programme of work were exactly a scanner that ran, failed and
blocked nothing.

**Paste the job id as written.** GitHub names a reusable workflow's check
`<caller job id> / <reusable job id>`, so the context depends on the id the
*caller* gives its job — a name core's own file cannot set. Calling the job
anything other than `core-pin-drift` produces a different context, and
protection requiring a name nothing reports holds every merge at
"Expected — waiting for status to be reported" until someone with admin access
notices. `internal/ciguard/core_pin_drift_test.go` fails if this document and
the workflow stop agreeing about the name.

The workflow takes **no inputs**. The threshold is one number for the fleet,
written down here, not a per-repository knob.

## The threshold, and the argument for it

| | |
|---|---|
| **Warning** | an update has been available for **3 days** |
| **Failure** | an update has been available for **10 days** |
| **Versions behind** | always reported, never a threshold |

### The baseline is core's newest TAG, never core's main

Merging into core is not shipping, which is the whole reason #891 exists. So
the gate never compares a pin against core's `main`: it compares against the
newest version the module proxy's `@v/list` reports, and that list is core's
published versions — tags. An unmerged or untagged commit on core's `main` does
not appear in it and cannot become the baseline, by construction rather than by
a filter that could be got wrong.

Concretely: the `iat` fix (#884, #888) became consumable when **v0.19.19** was
tagged. Before that tag existed the gate's baseline was v0.19.18 and no
repository was reported behind on account of those commits sitting on `main` —
correctly, because there was nothing to consume. The tag is what moved every
repository's clock.

### The clock runs on the oldest version you are missing

Not on the age of the pin. "Pinned to something six days old" is not the risk;
"a newer version has been available for six days and this repository has not
taken it" is. So drift is the age of the oldest version a module does not have.
That has three properties worth having:

- **Every core release starts one clock for the whole fleet**, not one per
  repository.
- **A burst moves nobody's clock.** Core has published 8 and 9 versions in
  single days; the oldest missing version is still that day's first, so a burst
  is one deadline rather than nine.
- **A partial bump does not reset it.** Moving from v0.18.3 to v0.19.0 while
  v0.19.18 exists leaves v0.19.1 as the oldest missing version, and its clock
  has been running since it was published.

### Why 10 days

[#891](https://github.com/codefly-dev/core/issues/891) names a security release
sitting unconsumed **for two weeks** as the case that actually matters, so the
gate has to fire inside two weeks. It also warns that a gate firing too early
"trains people to ignore it", and that is the real constraint: core published
164 versions in the 90 days to 2026-11-04, about 1.8 a day, so a tight
threshold means a fleet that is red most of the time.

10 days is the widest window that still fires before the fortnight #891 names.
It spans a weekend plus a working week, so a repository on any ordinary bump
cadence never sees red, and the longest gap between release days in core's last
180 days is 19 days — so a repository that was current stays green through the
quiet stretches.

3 days for the warning puts the number in front of a reader within the week
without blocking anything.

### Why there is no "more than N versions behind" failure

This is the obvious shape and it does not survive core's cadence. Measured over
all 460 version tags:

| window | most versions published |
|---|---|
| densest 3 days | 30 (2026-08-10 → 2026-08-13) |
| densest 10 days | 63 (2026-08-05 → 2026-08-15) |

Any count low enough to mean something therefore fires on a repository that is
three days behind during an ordinary burst — the "trains people to ignore red"
failure. Any count high enough to clear a burst is one the 10-day rule has
already failed long before. A threshold that can never be the deciding axis is
a decoration, so there is not one.

The count is still on every finding, because "eleven versions behind" is what
makes a reader act. It is a number, not a gate.

## What the failure message carries

Every finding states the pinned version, how many versions core has published
since, which the oldest missing one is, how long it has been available, and
what to change. A red check whose message does not tell the reader what to
change is how a gate gets ignored into uselessness.

```
  [FAIL] modules/robin/services/tool-owner/code/go.mod
         pinned to v0.18.3; core has published 19 version(s) since, the oldest of
         which (v0.19.0) has been available for 23 days

What to do

  `codefly agent deps --pin v0.19.18` from the repository root updates every
  lock this module owns, then tidies and verifies the build. It is the verb
  that owns the pin; a hand edit to go.mod reaches this file and leaves the
  nested fixtures and templates behind
    - modules/robin/services/tool-owner/code/go.mod
```

The same numbers go out as GitHub annotations, against the `require` line in
the Files view, and as a table on the run's summary page. The remedy names the
CLI verb rather than a `go get`, because the CLI owns the fleet verbs: a hand
edit reaches `go.mod` and leaves the nested base fixtures and factory templates
behind. The gate reports drift; it does not close it.

## Every go.mod, not the first

A repository's pins are wherever its modules are. The obin-ai modules pin core
in `modules/<name>/services/<svc>/code/go.mod` and nowhere near the root, and
one repository spans v0.5.11 to v0.19.11 across eleven of them. Across the nine
repositories that pin core there are **40 `go.mod` files at 18 distinct pins** —
where #891's table, reading one per repository, found nine at five.

`vendor`, `node_modules` and `.git` are skipped: those manifests belong to the
dependency. **`testdata` is not skipped.** A fixture pinned to an old core is
reported like anything else — in this fleet a `testdata` tree is sometimes a
real published module, and skipping by convention would leave the gate with no
opinion about a whole class of module. The advice for a fixture points at the
exemption rather than at a bump, because bumping a fixture breaks the test that
asserts against its contents.

## The pins that are not simply a version

A pin is not always "a release, possibly an old one", and reading each of these
as a number is how a gate reports green on something broken. 23 of the fleet's
40 pins are pseudo-versions, so these are the common case and not edge cases.

| pin shape | verdict | why |
|---|---|---|
| a release below the newest | warn / fail on the clock | the ordinary case |
| a **pseudo-version** (`v0.19.7-0.20261026092055-b23dc13543b2`) | at least a warning | an untagged commit on core's main. Nothing states what it contains, and core squash-merges, so it is one squash from the row below |
| a pseudo-version naming a commit **not on core's `main`** | **fail** | it sits on a branch, and core squash-merges: the merge destroys this exact commit. See below |
| a pseudo-version whose commit core no longer has | **fail** | the merge already happened; the pin fetches for nobody resolving direct |
| a pseudo-version above every release | warning | nothing is behind; it is still not a release |
| a version core **never published** | **fail** | see below |
| `replace` → a filesystem path | **fail** | the `require` line is not what builds, so the gate cannot answer from it |
| `replace` → another version | judged on the replacement | that version is what builds |

### A pin above the newest release is not "up to date"

At the time #891 was written, `codefly-dev/cli` appeared to pin `v0.19.19`
while core's newest tag was `v0.19.18`. Nothing resolved `v0.19.19` — not the
tag list, not the releases API, not the module proxy. A checker comparing
version numbers would have called that pin *newer than core* and therefore
fine, while `go mod download` could not fetch it at all. So the gate asks the
proxy whether a pin resolves rather than only where it sorts, and a plain
version core never published is a hard failure whose message says it sorts
*above* the newest release — precisely because that is what makes a numeric
comparison pass it.

(The real pin turned out to be the pseudo-version
`v0.19.19-0.20261104012958-be256d5c567d`, which does resolve: a commit on
core's `main` that no tag contained at the time. That is the row above — a
warning. **v0.19.19 has since been tagged**, which is what makes the `iat` fix
consumable at all; the pseudo-version now sorts just below that tag and reads
as one version behind. The failure shape stands on its own: a plain version the
proxy does not list is one nothing can resolve, whichever number it happens to
be.)

### A pinned commit must be reachable from core's `main`

On 2026-11-04 `obin-ai/module-robin`'s `main` went red for every Go job:
robin#659 squash-merged and deleted the branch holding a commit its own
`go.mod` pinned, and `go mod download` then failed repository-wide with
`unknown revision`. Robin's pin gate had passed on that commit — **it certified
a reference that merging would destroy.**

A pseudo-version naming a commit that is not an ancestor of the default branch
is exactly that reference. It resolves today, because the branch still exists.
It stops resolving the moment that branch is squash-merged and deleted. Core
squash-merges, so every such pin in this fleet is one merge away from dead.

So the gate asks GitHub whether the commit is an ancestor of core's `main`
(`compare/main...<sha>`) and reads `ahead_by`: zero means the commit is on
`main`; anything else means it sits on a branch, and the pin fails **before**
the merge that would destroy it. Two of the nine repositories were pinned this
way when the check was written.

**The module proxy cannot answer this, and that is worth being precise about.**
`proxy.golang.org` is a mirror: it caches a module version permanently and goes
on serving it after the commit is gone upstream. A proxy lookup succeeding
proves the proxy once saw the commit, never that the commit is still there — so
the orphan detection this gate started with could not have caught the robin
failure. The two services genuinely disagree, and one recorded commit proves
it: `9d2348c460ae` is not on core's `main`, and the proxy serves its
pseudo-version with a `200` anyway. Anything resolving direct — a private
module, `GOPRIVATE`, a fresh checkout without the mirror — gets the real
answer and fails. `TestTheProxyResolvesACommitThatIsNotOnTheDefaultBranch`
holds that disagreement.

The check needs no write access. CI passes `GITHUB_TOKEN` only for the
authenticated rate limit; the API answers for a public repository without one.
If the question **cannot** be answered — rate limit, network, a response shape
the gate does not recognise — the finding records that reachability went
unverified and is never reported clean, but the gate does not fail on it and
does not wedge: an unanswered question is not evidence of a destroyed
reference, and a rate limit must not hold nine repositories' merges.

### Why the module proxy and not GitHub Releases

Core has 460 version tags and 62 GitHub Releases: releases were only ever
created for the most recent six weeks of them. A gate counting GitHub Releases
is therefore wrong about 398 of the versions a consumer can actually pin, and
has no opinion at all about a pin like `v0.15.1`. `GOPROXY`'s `@v/list` is
exactly "what a `go.mod` may resolve", which is the question a pin asks, and it
is the same source the consumer's own build resolves core from — a gate
answering from a different source than the build is not a verdict on the build.
It also needs no credential, which keeps this gate out of
[the credential model](ci-credentials.md) entirely.

`GOPROXY` is honoured rather than hardcoded. With `GOPROXY=off` the gate has no
catalogue, so it exits **2** — distinct from the **1** it exits on drift,
because "this repository is behind" and "this gate could not answer" must not
read as the same thing, and neither may read as green.

So the gate reads two sources, each for the one question it can answer: the
**proxy** for what core has published and when, because that is what a `go.mod`
resolves against; and the **repository** for whether a commit is still part of
`main`, because the proxy's cache cannot tell.

## Exemptions

A module deliberately pinned away from core's newest version is declared in the
consuming repository, at `.github/core-pin.yaml`:

```yaml
exempt:
  - path: pkg/prerelease/testdata/...
    reason: fixtures assert against these pins; bumping them breaks the suite
  - path: libraries/candidate-sdk/go
    reason: frozen with the candidate SDK, bumped on its own release train
```

`path` is the module's directory, repository-root-relative. A trailing `/...`
covers that directory and everything under it; it is the only pattern accepted,
because a general glob would let one entry quietly cover modules added later.

Three things are refused rather than tolerated:

- **An exemption with no `reason`.** The gate's whole value is that a reader
  knows what to change, and an exemption nobody can explain is the same hole
  with the check removed.
- **An exemption matching nothing.** It is a recorded reason nobody is holding
  to anything, and the module it meant to cover may have moved under a name
  that is now unguarded.
- **An unknown key.** A typo for `reason` that decoded silently would produce
  an exemption with no reason at all.

An exempt module still appears in the report, with its reason.

## Running it outside CI

From a checkout of core, against any consumer:

```bash
make check-core-pin REPO=../cli
go run ./internal/corepindrift ../cli          # the same thing
go run ./internal/corepindrift -now 2026-12-01T00:00:00Z ../cli
```

## Where this sits

Core **reports** drift; the CLI **closes** it. `codefly agent deps --pin` owns
a consumer's locks, and `codefly agent release --pin` runs that consumer's CI
before anything is tagged — see
[`.claude/skills/release-core`](../.claude/skills/release-core/SKILL.md) for the
cascade. This gate holds no write credential and opens no pull requests: a gate
that bumped nine repositories itself would be core reaching into them with a
write token, which is a much larger thing than #891 asks for.
