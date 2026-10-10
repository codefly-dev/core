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

Both questions turn on what an expression MEANS, not on how it is spelled, and
a credential may be read without the word `secrets` followed by a name. So an
expression is parsed, a condition is evaluated, and a secret reference is found
by walking the syntax tree for any read of the `secrets` context — in whichever
form GitHub resolves it. Workflow-level `env:` is part of the model, because
every job inherits it.

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
it is unknown, since the event fixes only the prefix. Sampling a single ref
would answer for one instance and not for the event.

Where a scenario pins a party-chosen value on purpose, to oblige one particular
condition, it goes in a separate `adversarial` map with its reason, and a test
refuses any other binding of a party-chosen path.

Everything that cannot be read is refused rather than interpreted: an expression
that will not parse, a `permissions:` scalar or access level the reader does not
recognise, a job that cannot be re-encoded for the backstop, a repository-local
action with no loadable manifest. Each fails the guard that asked instead of
reporting "nothing found".

## The rule

**A job that receives a credential must be one of the shapes this repository
runs, recorded exactly, or it is refused.**

There is one list, in `internal/ciguard/job_templates_test.go`: the five jobs
that hold a secret or a write token, each pinned by a digest over the job's
canonical content and each named alongside the test that executes it. A job
that is not on the list is refused. A job on the list that has changed in any
respect — a step added, an input changed, a field no rule reads — no longer
matches its digest and is refused until someone updates the record, which is
the review step.

Nothing is analysed. Six rounds of asking what a job *does* — which contexts
are a source, which fields a sink, which commands execute a value — each closed
the routes it had found and left the ones it had not, because the set of things
a workflow can do is not a set a guard can finish. Asking whether a job *is* a
known shape has no such set.


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

## A separate, smaller rule: a `run:` is a program

Everything above is about credentials, and it is pinned by shape because the
question it answers — could this job's credential reach code nobody reviewed —
is one no analysis finishes. There is a second rule here that is not part of
that model and does not want its machinery.

**A `run:` script never interpolates a value chosen by whoever triggered the
run.** A `run:` is the one field whose content is a *program*: GitHub assembles
it before any shell sees it, so `${{ inputs.setup-run }}` written there is not
a value the script reads, it is a line of the script, already past every
quoting rule the author wrote. The value reaches the script through the
environment instead, which keeps the program fixed:

```yaml
env:
  SETUP_RUN: ${{ inputs.setup-run }}
run: eval "$SETUP_RUN"
```

`eval` is not a workaround for the env indirection — it is what preserves the
"arbitrary shell command" contract the input promises while leaving the value a
value. Shell `-e` still applies inside it, so a failing command still stops the
step.

This rule is checked by reading the parsed script rather than by pinning a
shape, and that is affordable here because the set is closed: the contexts
whose content the triggering party chooses are `inputs.*`, `github.event.*` and
`github.head_ref`. `runner.*` and `matrix.*` are fixed by the runner and by
this repository, so they are permitted. `env.*` and `steps.*` are a level of
indirection this rule does not follow — a tainted value can still arrive
through them, and that is what the credential model is for.

It is separate for a reason worth recording. The credential allowlist pins the
five jobs that hold a secret or a write token, so it covers a reusable workflow
holding a credential and says nothing about one holding none. Reverting
`go-service-ci.yml` to `run: ${{ inputs.setup-run }}` therefore left
`go test ./internal/ciguard/` green, while the identical change to
`go-service-release.yml` was refused by seven tests — the comments in both
files claimed the property, and only one of them had anything holding it.
`internal/ciguard/run_interpolation_test.go` now holds it for every workflow in
the directory, including ones added later, and proves on each case that it
fires rather than asserting a clean tree and being believed.

What it does not cover: the *absence* of a check. Deleting `agent-ci.yml`'s
semver validation of `codefly-cli-version` is not an interpolation, and no
guard refuses it — that file holds no credential, so it cannot join the
allowlist, and pinning its bytes would be a mechanism for one line.


## Adding a workflow

Declare `permissions: contents: read` at workflow scope and name no secret, and
stop. If a job needs to write or to hold a secret, give it its own
`permissions:` block, its own `if:` pinning it to a push of `main` or a tag,
and no checkout of anything a pull request supplied. Keep party-chosen values
out of every `run:` script, whether or not the job holds a credential. Running
`go test ./internal/ciguard/` tells you whether you got it right.
