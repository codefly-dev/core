# Protecting `main`

## Where protection lives: rulesets, not the legacy API

**Read this first, because this document used to say the opposite.** It opened
with "`main` has no branch protection" and told you to `PUT` a legacy
`branches/main/protection` object. Both were wrong, and wrong in opposite
directions: `main` *is* protected, by a **ruleset**, and following the old
instructions would have written a legacy protection object alongside it.

The two live in different APIs and neither mentions the other:

```sh
# 404 "Branch not protected" — and this means only that no LEGACY protection
# object exists. It is not a statement about whether the branch is protected.
gh api repos/codefly-dev/core/branches/main/protection

# The actual answer.
gh api repos/codefly-dev/core/rulesets --jq '.[] | {id, name, target, enforcement}'
gh api repos/codefly-dev/core/rulesets/24397257
```

A 404 from the first is how the claim above got written down. Across these
repositories the rulesets are:

| Repository | Ruleset | Id |
| --- | --- | --- |
| `codefly-dev/core` | `protect main` | 24397257 |
| `codefly-dev/sdk-go` | `protect main` | 24397258 |
| `codefly-dev/cli` | `main merge queue` | 23846762 |

### What `protect main` holds on core today

Queried, not inferred. Ruleset 24397257, `target: branch`,
`enforcement: active`, `bypass_actors: []`, conditions `refs/heads/main`:

- **`required_status_checks`** — `Build`, `Proto`, `Registry cache cold (go)`,
  `Registry cache cold (next)`, `Registry cache clean runner (go)`,
  `Registry cache clean runner (next)`. Six, not the seven below:
  `pnpm source evidence` is derived as safe to require and is **not** in the
  live ruleset.
- **`strict_required_status_checks_policy: false`** — a pull request that went
  green against an older `main` can land without being retested. The `"strict":
  true` recommended below is not what is applied.
- **`deletion`** and **`non_fast_forward`** — `main` cannot be deleted or
  force-pushed.
- **`pull_request`** with **`required_approving_review_count: 0`**. Changes go
  through a pull request, and nothing obliges anyone to read one.

That last line is load-bearing elsewhere. `internal/ciguard`'s credential
record (`job_templates_test.go`) is review friction: it refuses a registered job
whose shape changed until someone records the new one. With zero required
approvals that record is a changelog, not a gate — which is why the credential
decision does not rest on it. See [CI credentials](../ci-credentials.md).

**The good news, also verified:** `Build` runs `go test ./...` and `go.yml`
triggers on `pull_request` with no `paths:` filter, so `internal/ciguard` is a
required check on every change to `.github/`. The guard suite cannot be skipped.

## Why the set below matters

#447 merged with `Build: FAILURE` visible on the pull request; auto-merge was
not involved and nothing stopped it. That is the hole `required_status_checks`
closes, and it is closed now.

The cost of getting the *names* wrong is the reason this runbook exists. Pull
request CI tests the *merge* of the branch with `main`, so one red commit on
`main` turns every open pull request red regardless of its contents — and the
failure looks exactly like ordinary flakiness, so it is paid for by every
contributor at once before anyone diagnoses it. Meanwhile a required check whose
workflow never runs holds the merge open forever.

Rulesets are edited in **repository settings** or through the rulesets API,
neither of which this repository can reach. This runbook is the other half: the
check names that are safe to require, and the ones that will wedge every merge
if you require them.

## The set to require

```
Build
Proto
pnpm source evidence
Registry cache cold (go)
Registry cache cold (next)
Registry cache clean runner (go)
Registry cache clean runner (next)
```

`Build` is the strict signal from `go.yml` — the test suite, the race detector,
coverage, `govulncheck`, and the version/tag and CGO-free guards. It is the
check #447 was red on. `Proto` is the same workflow's `buf breaking` gate on
`proto/`, split out so a BSR hiccup and a schema break answer separately.
`pnpm source evidence` runs the real lockfile audit and inventory tests with the
pinned pnpm toolchain. The other four are `build-cache.yml`'s registry
cache-conformance matrix.

All seven qualify on the same three grounds, which are what make a required check
safe rather than merely desirable:

- **Their workflow runs on every pull request to `main`** — `on.pull_request`
  with no `paths:` filter.
- **Their names are fixed.** `Registry cache cold (${{ matrix.language }})`
  interpolates a matrix declared as the literal list `[go, next]`, so the two
  names it produces are knowable and stable.
- **They can only be skipped behind another required check.** `cache-warm`
  needs `cache-cold`; if `cache-cold` fails it has already blocked the merge, so
  the skip adds no new way to be stuck.

## Applying it

`protect main` already exists, so this UPDATES ruleset 24397257 rather than
creating anything. Fetch it first — a `PUT` replaces the whole object, so a
rule left out of the payload is a rule removed:

```sh
gh api repos/codefly-dev/core/rulesets/24397257 > /tmp/protect-main.json
```

Then put it back with the required checks set to exactly the derived set:

```sh
gh api -X PUT repos/codefly-dev/core/rulesets/24397257 --input - <<'JSON'
{
  "name": "protect main",
  "target": "branch",
  "enforcement": "active",
  "bypass_actors": [],
  "conditions": {"ref_name": {"include": ["refs/heads/main"], "exclude": []}},
  "rules": [
    {"type": "deletion"},
    {"type": "non_fast_forward"},
    {"type": "pull_request", "parameters": {
      "required_approving_review_count": 0,
      "dismiss_stale_reviews_on_push": false,
      "require_code_owner_review": false,
      "require_last_push_approval": false,
      "required_review_thread_resolution": false,
      "allowed_merge_methods": ["merge", "squash", "rebase"]
    }},
    {"type": "required_status_checks", "parameters": {
      "strict_required_status_checks_policy": true,
      "do_not_enforce_on_create": false,
      "required_status_checks": [
        {"context": "Build"},
        {"context": "Proto"},
        {"context": "pnpm source evidence"},
        {"context": "Registry cache cold (go)"},
        {"context": "Registry cache cold (next)"},
        {"context": "Registry cache clean runner (go)"},
        {"context": "Registry cache clean runner (next)"}
      ]
    }}
  ]
}
JSON
```

Verify with:

```sh
gh api repos/codefly-dev/core/rulesets/24397257 --jq '
  {enforcement, bypass: (.bypass_actors | length),
   rules: [.rules[].type],
   strict: (.rules[] | select(.type == "required_status_checks")
            | .parameters.strict_required_status_checks_policy),
   checks: [.rules[] | select(.type == "required_status_checks")
            | .parameters.required_status_checks[].context],
   approvals: (.rules[] | select(.type == "pull_request")
               | .parameters.required_approving_review_count)}'
```

Two differences from what is live, and both are decisions rather than
oversights to correct blindly:

`strict_required_status_checks_policy: true` is "require branches to be up to
date before merging". It stops a pull request that went green against an older
`main` from landing without being retested, and it costs a rebase per merge.
Live value: `false`.

`required_approving_review_count` stays `0` in the payload above because that is
what is live and because GitHub does not let you approve your own pull request —
on a single-maintainer repository, requiring one means nothing can ever merge.
Raising it is the owner's call, and it is the one change that would turn the
credential record in `internal/ciguard` from a changelog into a gate.

`bypass_actors: []` is the decision equivalent to the old `enforce_admins:
true`: with a bypass actor an admin can still merge red, which is how `main` got
to #447. It is empty today; keep it empty.

## What must stay out, and why

**`plan` and `build (…)` from `companions-build.yml`.** That workflow is
`paths:`-filtered to `companions/**` and friends, so a pull request touching
none of them never triggers it. A required check whose workflow does not run is
never reported, and GitHub holds the merge at "Expected — waiting for status to
be reported" indefinitely. This is the difference that matters: a check that
*fails* is recoverable by pushing a fix, while one that never arrives needs
admin access to clear. Its `build` job is also a matrix over
`include: ${{ fromJSON(needs.plan.outputs.companions) }}`, computed at run time,
so its names carry the companion versions of the day
(`build (proto, 0.0.14, companions/proto/Dockerfile, …)`) and change on every
version bump.

**`tag` from `version-tag.yml`.** It triggers on `workflow_run`, not
`pull_request`, and is gated on the test workflow having succeeded, so it
reports on no pull request at all.

**`Notify Slack` from `go.yml`.** Gated to a push of `main` for two reasons —
the webhook is empty on a Dependabot or fork run, and a repository secret must
not sit in the job that runs the suite — so it reports on no pull request. See
[CI credentials](../ci-credentials.md).

**`Coverage badge` from `go.yml`.** The coverage THRESHOLD is decided in
`Build`, which is required; this job only pushes the badge `README.md` renders,
and it is gated to a push of `main` so it reports on no pull request either. It
is a separate job because it is the one write in that workflow and the job that
runs the suite must not hold it — see
[CI credentials](../ci-credentials.md).

**`agent-ci`, `go-service-ci`, `go-service-release`, `publish-service-image`.** `workflow_call` only —
they run when a service repository dispatches them, never here.

**Approving reviews.** `required_approving_review_count` stays `0`
deliberately — see "Applying it" above for why, and for what it costs.

**Push restrictions.** No `restrictions` equivalent is applied.
`combine-deps.yml` uses
`DEPS_COMBINE_TOKEN` to push the `deps/combined` branch and open a pull request;
it never pushes to `main`, so it is unaffected by protection as configured here
and merges through the normal pull request path like anything else. An
allowlist added later must include that identity, or the weekly dependency
batch silently stops.

## Also needed: a tag-creation ruleset

Branch protection covers `main`. It says nothing about tags, and
`version-tag.yml` cuts a permanent, proxy-cached tag — so restrict who may
create one. The workflow's own guards keep its credential away from unreviewed
code (see [CI credentials](../ci-credentials.md)), but they cannot speak for any
other identity with push access.

**Status: NOT APPLIED — OPERATOR ACTION, OWNER-OWNED.** Nothing in this
repository can apply it, so no change here closes it and its absence is not a
code defect. A read-only ruleset query returns only the branch ruleset
`protect main` (24397257); no ruleset targets `refs/tags/v*`. It stays open
until someone applies one and records the result here.

Apply a ruleset targeting `refs/tags/v*` that restricts creation to the
Actions identity this workflow runs as, and denies updating and deleting
outright. Both halves matter: re-pointing a tag is how a commit ends up
carrying two versions, which `git describe` then reports ambiguously.

Until it is applied, the protection in place is the workflow's own: the tag job
refuses a commit that is not reachable from the default branch. That governs
this workflow's credential and no other identity's.

## Also needed: who may dispatch

`combine-deps.yml` is dispatchable, and `workflow_dispatch` is not restricted
to the default branch: a dispatch names a ref, and both the repository content
*and the workflow file itself* come from it. The dispatch trigger has since been removed from that workflow, so this
concerns any dispatchable workflow added later: a branch that edits the
workflow file is outside what anything in
the file on `main` can constrain.

That is a permissions question, not a YAML one. Restrict who can dispatch
workflows — or move the credential-bearing job into an environment with
required reviewers — and keep `DEPS_COMBINE_TOKEN` scoped to what the weekly
combination actually needs. See [CI credentials](../ci-credentials.md).

## Before you turn it on

Confirm the seven names against a recent **pull request** run rather than a
push to `main`, with `gh pr checks <n>` or:

```sh
gh api repos/codefly-dev/core/commits/<pr-head-sha>/check-runs \
  --jq '.check_runs[] | "\(.conclusion)\t\(.name)"'
```

Confirm them against a **pull request** run, not a push to `main`: the Slack
notification steps in `go.yml` are gated to push events, so a push run can be
red for a reason no pull request ever sees (#489). Pull request runs skip those
steps.

## Keeping the set honest

`internal/ciguard/branchprotection_test.go` derives this set from the workflows,
pins it, and checks the payload above against it. It fails in both directions,
and both are your job to resolve in the pull request that causes them:

- **A check leaves the set** — you added a `paths:` filter, a job-level `if:`, a
  `types:` without `opened`/`synchronize`, or a run-time matrix. Protection is
  now requiring a name that may never report, which blocks every merge.
- **A check joins the set** — you added a job that runs on every pull request.
  Protection does not cover it yet.

Either way, update `requiredChecks`, the payload above, **and** ruleset
24397257 in the same change. The ruleset is not in this repository, so nothing
else will notice.

What the test does NOT check is whether the payload above matches what is
actually applied — it cannot reach the API. The six-versus-seven gap on
`pnpm source evidence` is recorded at the top of this file for that reason, and
re-checking it is a `gh api` call, not a test run.

One shape to know when adding a **matrix** job: GitHub appends the matrix values
to the check name, so an unnamed `lint` job really reports as `lint (1.27)`. The
derivation refuses to guess those names and leaves the job out. Give the job an
explicit `name:` that interpolates every matrix axis — the way
`build-cache.yml` writes `Registry cache cold (${{ matrix.language }})` — and it
becomes requirable.
