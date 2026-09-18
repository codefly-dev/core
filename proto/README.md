# core/proto

Proto **source of truth** for the codefly ecosystem (`codefly/*` and `mind/*`).

This lives in-tree (rather than the standalone `codefly-dev/proto` repo, which is
being retired) so a schema change and its regenerated bindings ship in one PR —
no edit → `buf push` → regenerate-elsewhere dance, and no drift between the
published snapshot and the generated Go.

## Layout

- `codefly/` — domain + service protos (base, services/{agent,builder,runtime,code,tooling,toolbox}, cli, mcp, actions, observability)
- `mind/` — Mind AI service protos (v1, debug, gateway)
- `buf.yaml` / `buf.lock` — buf module config + external deps (googleapis, protovalidate)

## Regenerating

Edit the `.proto` files here, then from `core/`:

```bash
codefly generate proto --proto ./proto --output ./generated --local
```

That regenerates the Go bindings into `core/generated/go/` (pinned codegen
plugins + goimports). Commit the `.proto` change and the regenerated code
together.

`--local` does not pin buf itself — it execs whatever `buf` is on `PATH`, so the
output is reproducible only once that buf is the `Makefile`'s `BUF_VERSION`.
`make buf-install` puts exactly that one there; run it before regenerating. The
check targets cannot stand in for it — they run buf through `go run`, which puts
nothing on `PATH`. codefly-dev/cli#744 tracks pinning it in the tool, where it
belongs.

Python bindings (for the CLI) are produced via `generated/buf.gen.yaml` where
the BSR remote plugins are reachable.

## BSR

The module keeps its `buf.build/codefly-dev/proto` name so it can still be
`buf push`ed for any external consumer that resolves it from BSR. In-repo Go
consumers do not — they import `github.com/codefly-dev/core/generated/go/...`.

## Rules

- All codefly services are `v0` (still evolving); Mind services are `v1`.
- This is pre-customer: breaking changes are normal and compatibility shims are
  not carried. Update core source/bindings and every aggregate-workspace
  consumer together so the workspace and released artifact set stay atomic.
- CI runs `buf breaking` against `main` under the `PACKAGE` rules, so a removal
  or a type change is caught while moving a message between files in the same
  package is not. Run it before pushing:

  ```bash
  make buf-breaking
  ```

  It compares against the **merge base**, because that is what CI compares: CI
  checks out the pull request already merged into `main`, so a package `main`
  gained since you branched is on both of its sides. Against `main`'s tip that
  package is missing from your branch alone and buf calls it a deletion — a
  break reported on a branch that touched no `.proto`. Pass
  `BASE_REMOTE=<remote>` if your `origin` is a fork; the target refuses a remote
  that is not `codefly-dev/core` rather than answering from a stale baseline.
- CI also runs `buf lint` under `BASIC` + `COMMENTS`, so every message, field,
  enum, and enum value needs a comment. Run it before pushing:

  ```bash
  make buf-lint
  ```

  It went unenforced for as long as it was only written down here, and `proto/`
  accumulated 363 `COMMENTS` findings (#567) — enough that the next real one was
  indistinguishable from them. That is why it is a workflow step now.
- Every target runs the buf version pinned in the `Makefile`, which
  `internal/ciguard` holds equal to the workflow's and the companion's, and which
  no recipe may bypass with a literal version. A bare `buf` answers from whatever
  is on `PATH`, which is not the buf deciding whether the schema lands.
- Validation uses CEL via protovalidate (field constraints in the `.proto`s).
