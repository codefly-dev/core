# CGO Surface Contract

Core is the shared library for the entire codefly ecosystem, and several
downstream binaries must link **statically** (`CGO_ENABLED=0`) for alpine/scratch
targets — notably `codefly companion publish` and `codefly self build
--os/--arch`. cgo-ness is a property of the **import graph**, resolved at link
time: any binary that transitively imports a cgo-only package cannot be built
CGO-free, no matter how cleanly that package is separated.

cgo dependencies are insidious because they are **transitive and silent**. A new
plugin or a bumped dependency can pull a cgo-only package into the import graph,
and nothing fails until someone tries a `CGO_ENABLED=0` build far downstream —
long after the offending change merged. This document defines the CGO-free
surface as an **enforced contract in core**, so creep fails at the core PR that
introduces it, not at a downstream publish weeks later.

## The cgo surface

The entire cgo surface of the module is confined to **one package**:

| Package | What pulls in cgo | Reachable from the CGO-free surface? |
|---|---|---|
| `code/semantic` | tree-sitter runtime + grammar bindings (`github.com/tree-sitter/*`, `github.com/tree-sitter-grammars/*`, `github.com/dekobon/tree-sitter-groovy`); each grammar's `bindings/go` compiles C via cgo | No — only `code/codeserver` imports it, and only on the default (cgo) build; the `codefly_nosemantic` build drops the import |

There are **no other cgo dependencies**: no sqlite, no cgo language servers. The
only literal `import "C"` elsewhere is a runner test fixture
(`runners/golang/testdata/mod_cgo`), which is not part of the module build.

The base `code` package and its per-language servers (`NewGoCodeServer`,
`NewPythonCodeServer`, `NewRustCodeServer`, the TypeScript server) are CGO-free.
The tree-sitter analyzer is reached only through the `code.SemanticAnalyzer`
interface seam, installed via `code.WithSemanticAnalyzer(semantic.New())`.

## Ownership of the cgo/no-cgo switch

Package isolation is necessary but **not sufficient** to make a consumer
CGO-free: the consumer still needs a build-time switch to conditionally import
(or not import) `code/semantic`. Core owns that switch so consumers do not each
reinvent it:

```go
import "github.com/codefly-dev/core/code/codeserver"

srv := codeserver.New(root) // + optional code.ServerOption values
```

- **Default build** — `codeserver.New` installs the tree-sitter analyzer. The
  server answers source-semantics operations. The binary links with cgo.
- **`-tags codefly_nosemantic`** — `codeserver.New` returns a plain
  `DefaultCodeServer` that never imports `code/semantic`. The binary links with
  `CGO_ENABLED=0`. Source-semantics operations report an unsupported-operation
  failure.

The switch cannot live in package `code` itself: `code/semantic` imports `code`,
so a `code` → `code/semantic` import would be a cycle. `code/codeserver` is the
package that depends on both.

### Canonical build tag

`codefly_nosemantic` is the **canonical, cross-repo tag name** for this switch.
Downstream consumers (e.g. the CLI's static builds) pass `-tags
codefly_nosemantic` on CGO-free builds; they do not define their own tag.

**Loud-fail on accidental `CGO_ENABLED=0`.** The default (untagged) build still
imports `code/semantic`, so building it with `CGO_ENABLED=0` fails at link time
(`build constraints exclude all Go files` for the tree-sitter bindings). This is
deliberate: an accidental CGO-free build of the full server fails loudly rather
than silently dropping semantics. Dropping semantics is only ever an explicit
choice, expressed by the tag.

## Guardrail against creep

`scripts/check_cgo_free.sh` (Makefile: `make check-cgo-free`; CI: the "CGO-free
surface guard" step) builds every package in the module with `CGO_ENABLED=0
-tags codefly_nosemantic`, **except** an explicit allowlist. If a package outside
the allowlist stops linking CGO-free, the check fails in core with a clear
message.

### cgo allowlist

Packages permitted to require cgo, kept in sync with `CGO_ALLOWLIST` in
`scripts/check_cgo_free.sh`:

- `github.com/codefly-dev/core/code/semantic` — the tree-sitter semantic
  analyzer.

Adding an entry is a deliberate, reviewed decision. A new cgo dependency does
not belong in the CGO-free surface unless there is no alternative; when it is
genuinely optional, gate it behind `codefly_nosemantic` (as `code/codeserver`
does) instead of allowlisting the package.

## Out of scope

Moving tree-sitter semantics out-of-process (a separate cgo helper/agent so
core-linked consumers are fully CGO-free with no split anywhere) was considered
and deferred. It contradicts the in-process source-only path and adds a
process/IPC hop; the build-tag split delivers the CGO-free contract without that
cost. Revisit if the gateway moves per-service semantics out-of-process for
other reasons.
