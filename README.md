# `codefly.ai` core

![workflow](https://github.com/codefly-dev/core/actions/workflows/go.yml/badge.svg)
[![Go Report Card](https://goreportcard.com/badge/github.com/codefly-dev/core)](https://goreportcard.com/report/github.com/codefly-dev/core)
[![Go Reference](https://pkg.go.dev/badge/github.com/codefly-dev/core.svg)](https://pkg.go.dev/github.com/codefly-dev/core)
![coverage](https://raw.githubusercontent.com/codefly-dev/core/badges/.badges/main/coverage.svg)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

> Fundamentals for the `codefly.ai` ecosystem.

![dragonfly](docs/media/dragonfly.png)

## What this is

`core` is the shared library that every codefly agent, the CLI, and user
services depend on. It defines:

- **Resource model** — `Workspace`, `Module`, `Service`, `Endpoint`, `Agent`,
  `Environment` (`resources/`)
- **Architecture** — DAG-based dependency resolution (`architecture/`,
  `graph/`)
- **Network model** — port allocation, DNS, native/container/public access
  modes (`network/`)
- **Configuration flow** — typed configs passed between services as env
  vars (`configurations/`)
- **Agent system** — gRPC-based agent lifecycle: Builder, Runtime, Code
  (`agents/`)
- **Runners** — Native, Docker, and Nix execution environments with
  consistent process supervision (`runners/`)
- **Companions** — sidecar containers for proto, Go, Python, Node tooling
  (`companions/`)
- **Telemetry** — structured logging via `wool` (`wool/`)

## Install

```sh
go get github.com/codefly-dev/core
```

`core` is self-contained — no `replace` directives, no internal codefly
imports beyond its own module.

### Keep the pin current, and prove it

`core` owns the Work Context, the signed capability every v2 service verifies
on every call. A verification rule tightened here is in force in your
repository only once your pin moves — so a stale pin is a security posture, not
untidiness.

Move a pin with the CLI, never by hand: `codefly agent deps --pin v<version>`
updates every lock a module owns (`go.mod`, nested base fixtures, and their
factory templates) and verifies the build. A `go.mod` edit reaches the first of
those and silently leaves the rest behind.

To have CI say how far behind you are, add one line — a job calling the gate
`core` ships:

```yaml
# .github/workflows/core-pin-drift.yml
name: core-pin-drift

on:
  pull_request:
  push:
    branches: [main]

jobs:
  core-pin-drift:
    uses: codefly-dev/core/.github/workflows/core-pin-drift.yml@main
```

It checks **every** `go.mod` in the repository against core's newest **tag**
(never core's `main` — merging into core is not shipping), warns when an update
has been available for 3 days and fails at 10, and names the version, the gap
and the command to close it. It also refuses a pin to a core commit that is not
on core's `main`: such a commit resolves today and stops existing the moment its
branch is squash-merged, which is how a repository's `main` goes red for every
Go job at once.

Then require the context `core-pin-drift / drift` on your default branch —
until an administrator does, it reports without blocking anything.

Full behaviour, the argument for those numbers, and how to exempt a module
that is pinned elsewhere on purpose: [docs/core-pin-drift.md](docs/core-pin-drift.md).

## Quick example

```go
import (
    "context"
    "github.com/codefly-dev/core/resources"
    "github.com/codefly-dev/core/wool"
)

ctx := context.Background()
w := wool.Get(ctx).In("Example")

ws, err := resources.LoadWorkspaceFromDir(ctx, "/path/to/workspace")
if err != nil {
    return w.Wrapf(err, "load workspace")
}
w.Info("loaded", wool.Field("modules", len(ws.Modules)))
```

## Develop

```sh
# All tests
go test ./...

# Single package
go test ./resources/ -v

# With race detector
go test -race ./...
```

Tests gracefully skip when external prerequisites (Docker, Nix flakes ≥ 2.18)
are unavailable.

## License

MIT — see [LICENSE](LICENSE).
