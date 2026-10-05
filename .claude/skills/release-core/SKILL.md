---
name: release-core
description: Cut a new version of core and carry it through the fleet, or work out why a consumer rejects the version core reports. Use when asked to release, tag, bump the version, publish core, to update or rebuild the agents, to pin core across the agents or the CLI, when an install fails with "Codefly x.y.z does not satisfy package requirement", or when you are about to create or move a git tag in this repository.
---

# Releasing core

Releasing is **one file edit**:

```bash
# version/info.codefly.yaml
version: 0.3.36
```

Merge it to `main`. `.github/workflows/version-tag.yml` cuts `v0.3.36` from that
file once the test suite is green, on exactly the commit that passed.

That is the whole procedure. The rest of this is why the shortcuts are wrong.

One consequence worth knowing before you chase a release that did not appear:
the tag job cuts a tag **only** from a commit reachable from `main`, and only
when the green run it reacted to was a push to this repository. A green suite on
a branch produces no tag, and neither does one on a fork — by refusal, with the
sha in the log, not by silence. That is deliberate: the credential that creates
the tag must never run code that has not been merged. See
[CI credentials](../../../docs/ci-credentials.md).

## Never tag by hand

Tags used to be cut out of band while nothing bumped the file, and the two
drifted: the file sat at `0.3.8` while `v0.3.18` shipped, and at `0.3.20` while
`v0.3.24` shipped.

That is not cosmetic. `version.Version()` reads this file — it is embedded, and
there is **no `-ldflags` override**, so the file is authoritative in every
build. `composition/contracts.go` gates every package install on it, so a
lagging file rejects packages the tool can actually run:

> Codefly 0.3.20 does not satisfy package requirement >=0.3.24

If that is the failure you are chasing, the fix is the file, not the package
and not the contract check.

## Never move or reuse a tag

A tag is immutable in practice: the Go module proxy caches it and deleting it
does not un-publish it. Re-pointing one is how a commit ends up carrying two
versions — three already do, and `git describe --tags` on such a commit reports
the *lower* one, so any step deriving "the next version" from describe walks
backwards and re-cuts a published version.

To release a change, bump the file. The workflow declines to touch a tag that
already exists, and `./scripts/check_version_tag.sh` fails on every new
duplicate.

## Check before you push

```bash
make check-version-tag
```

The file may run **ahead** of the published tags — that is a release-prep bump
and it passes. Only behind is a bug. The same guard runs in CI's `Build` job.

## The green that matters

Tagging is gated on the `codefly core` workflow succeeding, not on a push to
`main`, because a tag on a red commit publishes a broken version permanently.
Do not try to make a release land faster by tagging around a failing check —
that failure is the gate doing its job.

Report the startup/lifecycle protocol and capabilities separately from the Core
version, using `agents/contract/contract.json`. An unchanged contract requires no
agent rebuild or fleet repinning. Reproducible dependency pins and explicit
artifact selections are not runtime compatibility gates. Qualify published
artifacts without workspace overrides before reporting downstream adoption.

## After the tag: carrying it through the fleet

Nothing here is a hand edit. Every consumer pin has a command that owns it, and
each one gates something an edit skips — so reach for the verb, not `go get` and
not a `go.mod` edit across repos.

**One agent, end to end.** `codefly agent release --pin v<version>` is the whole
cascade in one verb: it pins core, runs that agent's CI **before** anything is
tagged, bumps the manifest above the authoritative remote tag, opens a release PR
for a human to merge, then tags the merge commit and verifies the release actually
published a downloadable asset. Re-running is safe — an open PR is waited on, a
merged one is tagged, and a tagged release whose asset has not appeared is
re-verified rather than superseded. `--no-wait` opens the PR and stops.

The CI-before-tag order is the point. A fleet bump that edits `go.mod` by hand
discovers a broken agent after the tag is published, and a tag cannot be moved
(see above). Let the gate refuse first.

**Pin without releasing.** `codefly agent deps --pin v<version>` updates every
lock an agent owns — `go.mod`, nested base fixtures, and their factory templates —
then tidies and verifies the standalone build. A hand edit reaches `go.mod` and
silently leaves the fixtures and templates behind. `--all` applies it to every
agent under a directory tree. `--link` / `--unlink` swap an agent between local
core source and the published version; CI ignores `go.work`, so it always builds
against `go.mod`.

**The rest.** `codefly update workspace` moves every workspace service to its
latest compatible agent. `codefly update deps` updates external dependencies and
re-audits; it deliberately leaves first-party `codefly-dev` modules to the two
commands above.

**Order the cascade by what breaks.** A required proto field or any other change
a producer must fill breaks its producers at *verification*, not at build — so the
module owning that producer is a gate on the fleet, not a parallel track. Pin it,
release it, and only then bump the agents that depend on it. Read the release PR's
own notes for which consumers it names.

**Check where the fleet actually is before bumping it.** Agents drift several
minor versions behind, so "bump core" is rarely one edit per repo. `codefly agent
list` shows every agent pinned in the workspace and whether it resolves, and
`codefly agent versions` shows an agent's versions and their resolvability.

## Companions are a separate contract

Companion images are not released by this flow. They are built from their own
manifest (`codefly companion build --all`) and published by the CLI; see
`docs/runbooks/publish-companions.md`.

## In the PR

State the version you bumped to and what changed under it. If the release is
consumed by a pending change in another repo, say which — the tag appears only
after merge, so the consumer cannot be updated in the same breath.
