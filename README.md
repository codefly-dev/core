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
