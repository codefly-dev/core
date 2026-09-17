---
name: release-core
description: Cut a new version of core, or work out why a consumer rejects the version core reports. Use when asked to release, tag, bump the version, publish core, or when an install fails with "Codefly x.y.z does not satisfy package requirement", or when you are about to create or move a git tag in this repository.
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

## Companions are a separate contract

Companion images are not released by this flow. They are built from their own
manifest (`codefly companion build --all`) and published by the CLI; see
`docs/runbooks/publish-companions.md`.

## In the PR

State the version you bumped to and what changed under it. If the release is
consumed by a pending change in another repo, say which — the tag appears only
after merge, so the consumer cannot be updated in the same breath.
