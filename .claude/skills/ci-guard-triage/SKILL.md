---
name: ci-guard-triage
description: Diagnose a red check in core's CI when the failure is one of the bespoke guards rather than a normal test — the CGO-free surface guard, the darwin surface guard, the version/tag drift guard, the proto breaking-change gate, govulncheck, or the coverage threshold. Use when CI is red on a step you did not expect to touch, or when you are about to add an allowlist entry, a build tag, a suppression, or a threshold override to make a check pass.
---

# Triaging a CI guard

Each of these exists because something merged green and broke somewhere else —
a downstream `CGO_ENABLED=0` link, a user's install, a consumer's wire format.
They fail here so that does not happen. So the question is never "how do I get
past this", it is **what is this guard telling me**.

Every one of them has an escape hatch that makes it green without fixing
anything. Using one without saying so in the PR is the hack the fleet standard
is about.

## The guards, and the honest response to each

### CGO-free surface — `./scripts/check_cgo_free.sh`

A package that must link with `CGO_ENABLED=0` now reaches cgo through its import
graph. Almost always a new import that pulls in `code/semantic` (tree-sitter).

- **Fix:** drop the import, or take the build-tag-aware constructor
  `code/codeserver.New` so the CGO-free variant is selected under
  `-tags codefly_nosemantic`.
- **Hack:** adding your package to `CGO_ALLOWLIST`. That list is for packages
  that *are* cgo, not for packages that accidentally import one. An entry is a
  reviewed decision and must move `docs/cgo.md` with it.

### Darwin surface — the `GOOS=darwin go vet` step in `go.yml`

Every runner is ubuntu, so this is the only thing that compiles the darwin-only
files under `runners/` and `toolbox/launch`. A failure is a real type error on
macOS. Fix the darwin implementation; do not narrow the package list. Note that
this type-checks only — it does not *run* the darwin tests, so say so.

### Version/tag drift — `./scripts/check_version_tag.sh`

`version/info.codefly.yaml` is embedded and gates every package install through
composition's `minimum-codefly-version` check. Behind the published tags is a
bug; ahead is a release-prep bump and fine. See the `release-core` skill.

If it reports one commit carrying two version tags, that is not cosmetic —
`git describe` goes ambiguous and a release step can walk the version backwards.
Three are frozen as a known baseline. A **new** duplicate is yours to unwind,
and deleting a published tag does not un-publish it.

### Proto breaking-change gate — the `Proto` job

`buf breaking` under the `PACKAGE` rules, against `origin/main`.

- **Exit 100** is a verdict on your schema. This repo is pre-customer, so a
  break is allowed — but core source, the regenerated bindings, and every
  consumer move together. Do not carry a compatibility shim.
- **Any other non-zero** is buf itself failing (it resolves `buf.lock` deps from
  the BSR on every run). That is a flake; the job already retries three times.
  Do not read it as a schema answer.

Reproduce locally rather than guessing from the log:

```bash
git fetch origin main
cd proto && buf breaking --against "../.git#ref=origin/main,subdir=proto"
```

The buf version in `go.yml` must match the one `companions/proto/Dockerfile`
bakes — `internal/ciguard` fails when they drift. Move both.

### govulncheck — `./scripts/govulncheck.sh`

Fails only for an unsuppressed vuln that **has an upstream fix**, so the answer
is normally to take the fix: bump the dependency.

A `.govulncheck.yaml` suppression is correct only when there is no patched
release, or the path is verifiably unreachable — and the entry carries the
module, a real justification, and a `reviewed:` date. Adding one to silence a
finding you have not traced is the hack.

### Coverage and the race detector

The coverage step enforces `.testcoverage.yaml`. Write the test; do not add a
path override. A race-detector failure is a real bug — a `bytes.Buffer`
concurrent write in `NativeProc` flaked Linux CI for months before it was one.

## Before you call it diagnosed

Confirm the cause by restoring the broken state and watching it fail again. "It
went green when I changed X" is not a diagnosis, and these guards are exactly
the place where a coincidence looks like an answer.

State in the PR which guards you ran locally and which you did not.
