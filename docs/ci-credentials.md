# Where CI's write credential lives

Every job in this repository's workflows is handed a `GITHUB_TOKEN` whether it
asks for one or not. `permissions:` decides what that token can do, and GitHub
scopes it **per job** — never per step. So "the credential is scoped to the
steps that need it" has exactly one spelling here: the write is a **job of its
own that runs no code under review**, and every other job declares read.

That is the whole structure. The rest of this says where each write is, and
what keeps it away from code this repository has not reviewed.

`internal/ciguard/untrusted_code_test.go` enforces all of it under
`go test ./...`, so none of it needs a workflow step of its own.

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
   question from this side for every caller at once.

Rules 2 and 3 decide *whether* a job runs; rules 4 and 5 decide *what* it runs
when it does. Both halves are needed: with only the first, a mistake in an
expression is an execution, and with only the second, there is nothing to say
which runs were wanted.

## The writes, and why each one is safe

| Workflow | The write | What keeps it off unreviewed code |
| --- | --- | --- |
| `go.yml` | `coverage-badge` pushes `coverage.svg` to the `badges` branch, which `README.md` renders | Its own job, gated to a push of `main`. The `Build` job that runs the suite declares read and hands the profile over as an artifact. |
| `version-tag.yml` | `tag` creates the release tag and publishes the release | Event and head-repository guards; checks out the default branch; refuses a sha not reachable from it. |
| `combine-deps.yml` | `combine` pushes `deps/combined` and opens a pull request | `schedule`/`workflow_dispatch` only, so no pull request can trigger it, and it runs the default branch's code. Both writes are actually performed by `DEPS_COMBINE_TOKEN`. |

Every other job in every other workflow declares `contents: read`.

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

Declare `permissions: contents: read` at workflow scope and stop. If a job
needs to write, give it its own `permissions:` block and make sure it runs no
code under review — which, for anything a pull request can trigger, means an
`if:` that pins it to a push of `main`. Running `go test ./internal/ciguard/`
tells you whether you got it right.
