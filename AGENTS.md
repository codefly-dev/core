# Working in codefly-dev/core

`github.com/codefly-dev/core` (Go 1.27) is the shared library every codefly
agent, the CLI, and user services depend on. It owns the resource model, the
agent gRPC contracts, the network and configuration models, the proto schema
source of truth, and the runner/companion surfaces.

It does **not** own: the `codefly` CLI (`codefly-dev/cli`, a private module — do
not import it, and do not make CI depend on it), any individual agent
implementation, or user workspaces. Core defines contracts that those fill in.
When a change you need belongs to one of them, it gets made there.

## How to behave

Fleet standard — [handbook#68](https://github.com/obin-ai/handbook/issues/68).
These land hard here: core is a *library*, so a defect is observed downstream and
the temptation is always to absorb it locally rather than fix what owns it.

- **A gap in the tooling is a bug in the tooling — never a reason to reach around
  it.** When something needs a step `codefly`, `buf`, or a companion does not
  perform, the answer is a capability fixed in whichever tool owns it, and named
  in the PR. It is never a hand-assembled substitute — not as a "workaround", not
  "just this once", not "until the capability lands".
- **Never hack. Provide the best fix, even when it spans repos.** The fix living
  in `codefly-dev/cli` or an agent repo is not a reason to work around it here.
  Open the PR there and consume the reviewed result. When it genuinely cannot be
  fixed now, the deliverable is a precise issue against that owner plus an
  explicitly labelled stopgap — never an unlabelled one.
- **Classify every change that makes something work**, in the PR body: a *fix* at
  the place that owns the behaviour, or a *hack*. A hack does not become a fix by
  working, by being small, by being local, or by the real fix belonging elsewhere.
- **Never hardcode what the system resolves** — injected environment, derived
  ports, service addresses, credentials copied out of another component. Core is
  where that resolution is *defined*: ports come from `network.ToNamedPort()`,
  endpoints and credentials from the configuration flow. Typing one encodes
  something true only on one machine for ten minutes, and it fails quietly — a
  runtime missing a credential can skip registration *silently*, so the service
  boots, serves, and is simply absent.
- **Diagnose, do not pattern-match.** "It started working when I set X" is not a
  diagnosis — set X back and confirm it breaks. Do not trust an error message
  before checking its claim: a guard here reporting a version contract failure
  has meant a stale embedded file, not a bad package.
- **Say what you did not verify.** Unverified is not the same as working. A unit
  run is not the tagged suite, and neither is a boot; if you could not exercise
  something, the PR says so.

## Build and test

Derived from `.github/workflows/go.yml` — CI pins Go 1.27.0 and every step is
strict (nothing skips when a prerequisite is missing).

```bash
go test ./...                                      # the suite
go test ./resources/ -v                            # one package
go test -race ./... -timeout 10m                   # CI runs this separately
make check-coverage                                # thresholds in .testcoverage.yaml
```

CI additionally runs, and all of these are runnable locally:

| Command | Guards |
| --- | --- |
| `go test ./... -tags=sandbox_e2e,nix_required` | needs `bubblewrap`, `ripgrep`, `uv`, `nix` |
| `./scripts/check_cgo_free.sh` (`make check-cgo-free`) | cgo creep outside `code/semantic` |
| `./scripts/check_version_tag.sh` (`make check-version-tag`) | `version/info.codefly.yaml` vs. published tags |
| `./scripts/govulncheck.sh` | unsuppressed vulns that have an upstream fix |
| `make buf-breaking` (and `make buf-lint`) | schema breaks, `PACKAGE` rules |

Run buf through those targets, never a bare `buf`: they pin the version CI's
gate and the companion's generator both use, so a local check is a verdict on
the gate rather than on whatever is on `PATH`.

`GOFLAGS ?= -timeout=300s` is exported by the `Makefile` so no test run hangs.
Tests with the `proto_companion_required` tag are **not** in CI — they need an
image core alone cannot build. Run them locally before touching that surface.

**Never mock.** Tests use real infrastructure and real YAML fixtures under
`testdata/`. If a boundary is hard to reach, reach it anyway.

## Where things live

`resources/` is the source of truth for every type. When unsure how something is
modelled, read it there first.

| Path | Owns |
| --- | --- |
| `resources/` | Workspace/Module/Service/Endpoint/Agent, YAML loading, validation |
| `proto/` | schema source of truth (`codefly/*`, `mind/*`); `generated/` is output |
| `agents/` | agent registration, process manager, the embeddable server types |
| `network/` | deterministic port allocation, DNS, native/container/public modes |
| `configurations/` | what services provide and consume, injected as env vars |
| `runners/`, `companions/` | process execution; sidecar images for language tooling |
| `code/semantic` | the **only** package allowed to use cgo (tree-sitter) |
| `internal/ciguard` | invariants about CI config that no other test would notice |

The deeper picture is in [`docs/architecture.md`](docs/architecture.md) —
resource hierarchy, agent lifecycle, network mapping, configuration flow,
readiness, dependency graph. Read it rather than re-deriving from the tree.

## Rules that bite

- **Ports come from `network.ToNamedPort()` or `RuntimeManager`.** Never
  hardcode one, always track allocations — a missing dedup once assigned the
  same port twice.
- **Readiness is a gRPC health check, never a TCP connect.** An open port does
  not mean a ready service. Endpoints declare `health.readiness`; consumers go
  through `resources.PlanReadiness` and the `readiness` package. See
  [`docs/readiness.md`](docs/readiness.md).
- **Regenerate protos with `codefly generate proto --proto ./proto --output
  ./generated --local`.** `--local` pins the codegen plugins and runs goimports.
  It does not pin buf — it execs whatever is on `PATH` — so install the
  `Makefile`'s `BUF_VERSION` first, or the bindings you commit were generated by
  a buf nothing here agrees with (codefly-dev/cli#744). Do not hand-run
  `buf`/`protoc`. Commit the `.proto` change and the regenerated bindings
  together; see [`proto/README.md`](proto/README.md).
- **Keep the CGO-free surface CGO-free.** cgo lives only in `code/semantic`;
  consumers take the build-tag-aware constructor from `code/codeserver.New`
  (`-tags codefly_nosemantic`). See [`docs/cgo.md`](docs/cgo.md).
- **Companions are ours.** A broken companion gets fixed, not routed around.

## Procedures

Step-by-step procedures live in `.claude/skills/`, loaded on demand rather than
carried here:

- `ci-guard-triage` — one of the bespoke CI guards is red, and you need to tell
  a real finding from an escape hatch that would only make it green.
- `release-core` — cutting a version, and why the tag is never made by hand.

## Workflow

- Branch and PR; never commit to `main`. Conventional Commits for the title.
- `internal/ciguard` holds the meta-invariants, including this file's length
  budget and each skill's frontmatter contract. It runs under `go test ./...`,
  so CI enforces it with no workflow change.
- Keep this file under ~150 lines (hard cap 200). Push depth into a nested
  `AGENTS.md` beside what it describes, into `.claude/skills/`, or into `docs/`.
- `CLAUDE.md` is a pointer to this file. Keep one canonical source.
- Treat this file as code: the PR that changes a process updates it.
