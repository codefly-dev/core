# Protecting `main`

`main` has no branch protection, so any pull request can merge while its checks
are red. #447 did: `Build: FAILURE` was visible on the pull request, GitHub
auto-merge was not involved, and nothing stopped the merge.

That is not a one-off cost. Pull request CI tests the *merge* of the branch with
`main`, so one red commit on `main` turns every open pull request red regardless
of its contents — and the failure looks exactly like ordinary flakiness, so it
is paid for by every contributor at once before anyone diagnoses it.

Protection is applied in **repository settings**, which this repository cannot
reach. This runbook is the other half: the check names that are safe to require,
and the ones that will wedge every merge if you require them.

## The set to require

```
Build
Proto
Registry cache cold (go)
Registry cache cold (next)
Registry cache clean runner (go)
Registry cache clean runner (next)
```

`Build` is the strict signal from `go.yml` — the test suite, the race detector,
coverage, `govulncheck`, and the version/tag and CGO-free guards. It is the
check #447 was red on. `Proto` is the same workflow's `buf breaking` gate on
`proto/`, split out so a BSR hiccup and a schema break answer separately. The
other four are `build-cache.yml`'s registry cache-conformance matrix.

All five qualify on the same three grounds, which are what make a required check
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

```sh
gh api -X PUT repos/codefly-dev/core/branches/main/protection --input - <<'JSON'
{
  "required_status_checks": {
    "strict": true,
    "checks": [
      {"context": "Build"},
      {"context": "Proto"},
      {"context": "Registry cache cold (go)"},
      {"context": "Registry cache cold (next)"},
      {"context": "Registry cache clean runner (go)"},
      {"context": "Registry cache clean runner (next)"}
    ]
  },
  "enforce_admins": true,
  "required_pull_request_reviews": null,
  "restrictions": null
}
JSON
```

All four top-level keys are required by the API even when null. Verify with:

```sh
gh api repos/codefly-dev/core/branches/main/protection \
  --jq '{strict: .required_status_checks.strict,
         checks: [.required_status_checks.checks[].context],
         admins: .enforce_admins.enabled}'
```

`"strict": true` is "require branches to be up to date before merging". It is
what stops a pull request that went green against an older `main` from landing
without being retested, and it costs a rebase per merge.

`"enforce_admins": true` is the decision to actually make. Without it an admin
can still merge red, which is how `main` got here — every merge in recent
history is by the same admin account.

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

**`agent-ci`, `go-service-ci`, `go-service-release`.** `workflow_call` only —
they run when a service repository dispatches them, never here.

**Approving reviews.** `required_pull_request_reviews` stays null deliberately.
GitHub does not let you approve your own pull request, and on a
single-maintainer repository requiring an approval means nothing can ever merge.

**Push restrictions.** `restrictions` stays null. `combine-deps.yml` uses
`DEPS_COMBINE_TOKEN` to push the `deps/combined` branch and open a pull request;
it never pushes to `main`, so it is unaffected by protection as configured here
and merges through the normal pull request path like anything else. An
allowlist added later must include that identity, or the weekly dependency
batch silently stops.

## Before you turn it on

Confirm the five names against a recent **pull request** run rather than a push
to `main`, with `gh pr checks <n>` or:

```sh
gh api repos/codefly-dev/core/commits/<pr-head-sha>/check-runs \
  --jq '.check_runs[] | "\(.conclusion)\t\(.name)"'
```

Push runs on `main` are currently red for a reason unrelated to any pull
request: `go.yml`'s Slack notification steps, which are gated to push events,
fail on the bumped action (#489). Pull request runs skip those steps, so `Build`
is green there and requiring it is safe today — but do not read a push run and
conclude the set is wrong.

## Keeping the set honest

`internal/ciguard/branchprotection_test.go` derives this set from the workflows,
pins it, and checks the payload above against it. It fails in both directions,
and both are your job to resolve in the pull request that causes them:

- **A check leaves the set** — you added a `paths:` filter, a job-level `if:`, a
  `types:` without `opened`/`synchronize`, or a run-time matrix. Protection is
  now requiring a name that may never report, which blocks every merge.
- **A check joins the set** — you added a job that runs on every pull request.
  Protection does not cover it yet.

Either way, update `requiredChecks`, the payload above, **and** the repository
settings in the same change. The settings are not in this repository, so nothing
else will notice; the failure lands on `main` only if the change merges without
protection turned on.

One shape to know when adding a **matrix** job: GitHub appends the matrix values
to the check name, so an unnamed `lint` job really reports as `lint (1.27)`. The
derivation refuses to guess those names and leaves the job out. Give the job an
explicit `name:` that interpolates every matrix axis — the way
`build-cache.yml` writes `Registry cache cold (${{ matrix.language }})` — and it
becomes requirable.
