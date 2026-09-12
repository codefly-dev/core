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

That regenerates the Go bindings into `core/generated/go/` (pinned plugin
versions + goimports → byte-reproducible). Commit the `.proto` change and the
regenerated code together.

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
- `buf lint` before committing.
- Validation uses CEL via protovalidate (field constraints in the `.proto`s).
