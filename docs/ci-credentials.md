# Where CI's credentials live

A job holds credentials of two kinds, and they need different answers.

The **built-in token** is handed to every job whether it asks or not;
`permissions:` decides what it can do, and GitHub scopes it **per job**, never
per step.

A **repository secret** — a PAT, a webhook URL — is a different object, and
`permissions:` says nothing about it at all. A PAT is as privileged as whoever
issued it. So narrowing the built-in token in a job that also names a PAT
achieves nothing: it puts a narrow credential beside a wide one.

Both therefore land on the same structure, for the same reason — there is no
per-step scoping — and it is the only structure available:

> **The credential lives in a job of its own that runs no code under review.**

Everything else declares read and names no secret. The rest of this says where
each credential is and what keeps it away from code this repository has not
reviewed.

`internal/ciguard` enforces it under `go test ./...`, so none of it needs a
workflow step: `untrusted_code_test.go` for what the built-in token can do, and
`credential_isolation_test.go` for whether a secret is reachable from code under
review. The second reads the shell scripts a job invokes, not only the workflow,
because that is where two of the routes were.

Both decide their questions from a **parsed expression**, not from its text
(`expression_test.go`). That is not fussiness — a guard that reads text answers
wrongly in both directions:

| Looks safe, is not | Why text cannot tell |
| --- | --- |
| `if: <safe condition> \|\| true` | contains every required clause; runs on a pull request |
| `if: ${{ !(always() && <safe condition>) }}` | contains every required clause, negated |
| `env: { X: "${{ secrets['NAME'] }}" }` | reads a secret; matches no `secrets.NAME` pattern |
| `env: { X: "${{ toJSON(secrets) }}" }` | reads *every* secret; names none |
| workflow-level `env:` | inherited by every job while appearing in none of them |

So a condition is **evaluated** in three-valued logic against scenarios that
bind the hostile facts — a pull request, a merge-queue candidate, a
`workflow_run` produced by a pull request, a `workflow_run` produced by a push
to a fork, a pushed tag. Anything the evaluator cannot know
(`needs.build.result`, `success()`) is **unknown**, and unknown is not false, so
a job is accepted only when its condition is *provably* false.

The evaluator implements GitHub's semantics, not an approximation of them, and
the differences are each a condition somebody can build:

- **`&&` and `||` return the selected operand**, not a boolean. Reduced to
  booleans, `(github.event_name == 'pull_request' && 'run' || '') == 'run'`
  compares `true` with `'run'` and reads false; GitHub selects `'run'` and runs
  the job.
- **`==` coerces across types.** Operands of different types are both converted
  to numbers, so `'' == 0`, `false == 0` and `true == 1` all hold, and a
  non-numeric string is NaN and equals nothing.

**What a scenario may bind is the soundness argument.** A scenario has to answer
for every instance of its situation, not for one, so it binds only values the
*event* determines (`github.event_name`, the repository, the default branch).
A value the triggering party chooses — `github.head_ref`, an upstream run's
`head_branch` — is left **unknown**, because binding one guess makes a
comparison against any other literal read as definitely-false and accepts a job
that is perfectly reachable.

Some facts sit in between, and a sample is wrong for those too: the event fixes
`github.ref` to `refs/pull/<n>/merge` for a pull request and `refs/tags/<name>`
for a tag push, while the number and the name are chosen. Those are bound as a
**domain** — the prefix the event fixes — so a comparison outside it is false
(`github.ref == 'refs/heads/main'` cannot hold on a pull request) and one inside
it is unknown (`github.ref == 'refs/pull/8/merge'` is true on pull request 8).
Sampling one ref made that second case definitely-false and accepted the job.

Where a scenario pins a party-chosen value on purpose, to oblige one particular
condition, it goes in a separate `adversarial` map with its reason, and a test
refuses any other binding of a party-chosen path.

Everything that cannot be read is refused rather than interpreted: an expression
that will not parse, a `permissions:` scalar or access level the reader does not
recognise, a job that cannot be re-encoded for the backstop, a repository-local
action with no loadable manifest. Each fails the guard that asked instead of
reporting "nothing found".

## The rules

1. **No workflow grants a write at workflow scope.** A workflow-scope grant is
   inherited by every job in the file, including the one added next year. The
   write is declared on the job that performs it.
2. **No job that can run a pull request's code holds a write.** A
   `pull_request` run checks out that pull request's head; every step after the
   checkout is running code its author wrote. A write-capable job in such a
   workflow must be gated `if: github.event_name == 'push' && github.ref ==
   'refs/heads/main'`.
3. **A `workflow_run` job states its event and its head repository.** The
   `branches:` filter matches the *triggering* run's head branch, which is a
   name the head repository chooses — it never meant "main of this repository".
4. **A `workflow_run` job checks out the default branch, not the triggering
   commit.** The triggering sha is data, validated against the default branch's
   history before use.
5. **A reusable (`workflow_call`) workflow declares read on every job.** It runs
   the *calling* repository's code, at a ref that repository chooses. A
   declaration here can only narrow what the caller granted, so it settles the
   built-in token from this side for every caller at once. It does **not**
   settle a secret the caller passes — see rule 6.
6. **No job that can run code under review names a secret.** Not gated by a
   step `if:` — a step condition decides whether a step runs, not what the job
   is. A job holding a secret runs on a merged ref:
   `github.event_name == 'push'` together with either
   `github.ref == 'refs/heads/main'` or `startsWith(github.ref, 'refs/tags/')`.
   A `workflow_call` workflow counts as reachable from a pull request, because
   a caller may dispatch it from one and nothing here can see that it did.

   **Which triggers count is decided the other way round.** A trigger is
   reachable unless it has been argued unable to carry a pull request's code
   (`push`, `create`, `delete`, `release`, `schedule`, `workflow_dispatch`, and
   `workflow_run`, which has stricter scenarios of its own). Listing the
   dangerous triggers instead would exempt every one nobody thought of —
   `issue_comment` and `pull_request_review` both carry a pull request's
   context, and GitHub keeps adding events.
7. **A tag is not a statement about review.** A tag can be pushed to any
   commit, so a credential-bearing job gated on one must prove the commit is
   reachable from the default branch with `git merge-base --is-ancestor`.
8. **A credential-bearing job never reaches for `refs/pull/`.** A pull
   request's head is whatever was last pushed to that branch; filtering by
   pull-request author establishes who opened it, not who wrote the commits on
   it. Assembling such a tree is legitimate work — it belongs in a job with no
   secret, which passes the result on as an artifact.
9. **A credential-bearing job in a dispatchable workflow checks out `main`
   explicitly.** `workflow_dispatch` is not restricted to the default branch.

Rules 2, 3, 6, 7 and 9 decide *whether* a job runs; rules 4, 5 and 8 decide
*what* it runs when it does. Both halves are needed: with only the first, a
mistake in an expression is an execution, and with only the second, there is
nothing to say which runs were wanted.

Rule 6's "runs on a merged ref" is decided by evaluating the condition, so any
spelling that is provably false on a pull request satisfies it and no list of
blessed clauses is needed. The two hostile `workflow_run` shapes are asserted
separately because each is refused by a *different* condition — a job carrying
only the event condition is still reachable from a fork's push, and vice versa.

## The writes, and why each one is safe

| Workflow | The credential | What keeps it off unreviewed code |
| --- | --- | --- |
| `go.yml` | `coverage-badge` pushes `coverage.svg` to the `badges` branch, which `README.md` renders | Its own job, gated to a push of `main`. `Build` declares read and hands the profile over as an artifact. |
| `go.yml` | `notify` holds `SLACK_WEBHOOK_URL` | Its own job, gated to a push of `main`, with `permissions: {}` and no checkout at all. It reads the outcome from `needs.build.result`. |
| `version-tag.yml` | `tag` creates the release tag and publishes the release | Event and head-repository conditions; checks out the default branch; refuses a sha not reachable from it. |
| `go-service-release.yml` | `goreleaser` receives the **caller's** release PAT | Gated to a pushed tag, which a pull request cannot produce; then refuses a tag whose commit is not reachable from the caller's default branch. The `test` job that runs the caller's suite names no secret. |
| `combine-deps.yml` | `publish` holds `DEPS_COMBINE_TOKEN` | A job of its own that never checks the combined tree out: the commits arrive as a git bundle and move into a ref by `git fetch`/`git push`. The `plan` job does the replaying, with the read-only built-in token and no secret. Both checkouts pin `ref: main`. |

Every other job in every other workflow declares `contents: read` and names no
secret. `secrets.GITHUB_TOKEN` is not a secret for this purpose: it is present
whether or not it is named, and `permissions:` governs it.

## Why the dependency combination is two jobs

`plan` fetches `refs/pull/<n>/head` for every open Dependabot pull request,
replays them onto one branch, and runs the repository's optional
`combine-deps-local.sh` hook over the result. That is unreviewed code, so the
job has no secret and a read-only token.

`publish` pushes the branch and opens the combined pull request. It holds the
PAT and never checks the assembled tree out — the commits reach it as a bundle,
and `git fetch` moves a ref without moving `HEAD`.
`internal/ciguard/combine_deps_handoff_test.go` runs those two git commands,
lifted out of the scripts, against real repositories and asserts the
publisher's working tree never changes.

One limit this cannot fix: a `workflow_dispatch` against a branch takes the
**workflow file** from that branch too, so a branch that edits the workflow is
outside what any assertion about the file on `main` can reach. Restricting who
may dispatch is a repository setting — see
[branch protection](runbooks/branch-protection.md).

## The tag job in full

It is the only one that writes to a ref, so it is worth reading end to end.

The trigger is `workflow_run` on the test workflow, because a tag must not be
cut from a red commit and a tag is permanent once the module proxy has cached
it. That trigger is also the one that can be reached from a run this repository
did not produce, so three things hold rather than one:

- the `if:` requires the triggering run to have **succeeded**, to have come
  from a **push**, and for its **head repository to be this one**;
- the checkout takes the **default branch** — no `ref:` — so the only code the
  job can execute is code already on `main`;
- the triggering sha is then used **only after** `git merge-base --is-ancestor`
  shows it is reachable from `origin/main`. A sha that is not a commit here, or
  that is a commit on no branch, is refused rather than tagged.

Then the job selects that commit with `git switch --detach` and reads the
version file and the contract manifest from it, because the release has to
describe the commit that passed rather than whatever `main` has moved on to.

`internal/ciguard/version_tag_selection_test.go` lifts that refusal out of the
workflow and runs it against a real repository, including the case the existence
check cannot catch: a commit that exists locally but is reachable from no
branch.

The remaining gap is not in this repository: nothing stops a tag being created
by some other identity. That is a tag-creation ruleset, which an operator
applies — see [branch protection](runbooks/branch-protection.md).

## Adding a workflow

Declare `permissions: contents: read` at workflow scope and name no secret, and
stop. If a job needs to write or to hold a secret, give it its own
`permissions:` block, its own `if:` pinning it to a push of `main` or a tag,
and no checkout of anything a pull request supplied. Running
`go test ./internal/ciguard/` tells you whether you got it right.
