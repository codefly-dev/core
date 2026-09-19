# proto companion

The codefly proto companion bundles `buf`, `protoc`, `protoc-gen-*`,
`swagger`, `grpcio-tools`, and the JS/TS generators into one OCI
image. Used by `codefly generate proto` and the
`codefly generate openAPI` pipeline.

## Build

There are two build definitions. CI publishes the **Dockerfile** one: the
flake emits a `linux/*` OCI tarball that needs a Linux builder and a
`docker load` + retag round-trip, which buys nothing on a Linux runner that
already has BuildKit and can cross-build `linux/arm64` in the same invocation.
Nix stays the reproducible local path and targets the same
`ghcr.io/codefly-dev/proto:<version>` tag, so either builder produces the
image the agents pull. `companion_plugins_test.go` keeps the two definitions
pinned to the same tool versions. Publishing is
[`docs/runbooks/publish-companions.md`](../../docs/runbooks/publish-companions.md).

After changing buf, bump the image version and build it, then run
`go test ./internal/ciguard -tags=nix_required,proto_companion_required` from
the repository root. These checks evaluate the Nix compiler on both Linux
architectures and execute buf in the selected Docker image; a matching
Dockerfile alone does not update an already published or cached image.
`nix build .#buf` in this directory verifies buf's source and dependency hashes
without rebuilding the other companion tools (requires a Linux builder).

`protoc-gen-es` is the one tool version this repository does not get to choose
on its own — it has to match the `@bufbuild/protobuf` runtime consumers pin, or
their committed `*_pb.ts` drift. The current pin, why it is where it is, and how
to move it are
[`docs/runbooks/bump-protobuf-es.md`](../../docs/runbooks/bump-protobuf-es.md).

### Nix (preferred locally — reproducible, layered cache)

```sh
# From core/companions/proto/, with a Linux build target.
# On macOS, requires nix-darwin's linux-builder configured.
nix build .#dockerImage
nix run .#streamDockerImage | docker load
```

The Nix flake pins every transitive dependency by content hash via
`flake.lock`. The dev shell (`nix develop`) uses the same package set
the image ships — no drift between "works on my dev machine" and
"the agent's image." Image tag is read from `info.codefly.yaml` so
the existing `tag.sh` version-bump flow continues to work.

### Dockerfile (what CI publishes)

```sh
codefly companion build proto
```

The Dockerfile assembles the same set via apk + `go install`. Less
reproducible than Nix (apk packages vary across Alpine releases) but
needs no Linux builder VM, which is why the publish workflow uses it.

## What's in the image

| Tool                          | Purpose                                  |
|-------------------------------|------------------------------------------|
| `buf`                         | Modern proto compilation + linting       |
| `protoc` + `libprotoc`        | Classic proto compiler (some plugins still need it) |
| `protoc-gen-go` / `-go-grpc`  | Go bindings + gRPC                       |
| `protoc-gen-grpc-gateway`     | REST-from-gRPC                           |
| `protoc-gen-openapiv2`        | OpenAPI from gRPC                        |
| `protoc-gen-connect-go`       | Connect-RPC bindings                     |
| `swagger` (go-swagger)        | OpenAPI client generation                |
| `go` + `gofmt`                | Required at runtime by `swagger` for source formatting |
| `grpcio-tools` (python)       | `grpc_python_plugin` for Python bindings |
| `node` + `npm`                | TypeScript generators                    |

## Generation ends with a formatting pass

`buf generate` is not the last step. The Go the plugins emit differs from the
Go every consumer commits: protoc-gen-go leaves the stdlib imports sorted in
among the third-party ones, and protoc-gen-grpc-gateway imports a sibling
package by bare path even when that package's name is not its path's last
element. Consumers run goimports over that and gate their checked-in bindings
on the result — the stdlib group split off, the alias added.

So the companion runs `goimports -w` itself, inside the image, over the
generated Go files in every output directory the template declares
(`companions/proto.FormatGoOutputs`). Its output is the committed shape, and a
consumer needs nothing on the host to reproduce a clean tree. goimports is
pinned in both build definitions like a plugin, because a formatter that moves
moves every consumer's tree; `TestDockerfilePinsGoimports` and
`TestFlakePinsGoimports` hold the two to the same version. Output directories
may also contain handwritten Go, so only files carrying Go's standard
`Code generated … DO NOT EDIT.` ownership notice are formatted.

## Versioning

`info.codefly.yaml` carries the image version (`0.0.10` at time of
writing). Bump via `scripts/tag.sh` — same convention as every other
companion. Both the Dockerfile build and the Nix build read this
value so the tag stays consistent.
