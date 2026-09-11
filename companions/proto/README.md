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

## Client languages

`GenerateClient` and `GenerateGRPC` target Go, Python, and TypeScript. **Rust is
refused.** Its plugins (`neoeinstein-prost`, `neoeinstein-tonic`) were resolved
remotely from the BSR rather than baked into this image, and BSR execution picks
generation targets by buf module identity — so a type reached through a
`buf.yaml` dependency is never generated, and prost, which has an extern path
only for `google.protobuf` (supplied by `prost-types`), drops the field from the
struct rather than failing. A contract referencing `google.rpc.Status` produced a
struct with no `status` field, silently losing that tag on both decode and
encode.

Re-enabling Rust means baking `protoc-gen-prost` and `protoc-gen-tonic` into this
image, where the image's own import markers decide what is generated.

## Versioning

`info.codefly.yaml` carries the image version (`0.0.10` at time of
writing). Bump via `scripts/tag.sh` — same convention as every other
companion. Both the Dockerfile build and the Nix build read this
value so the tag stays consistent.
