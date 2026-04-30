# proto companion

The codefly proto companion bundles `buf`, `protoc`, `protoc-gen-*`,
`swagger`, `grpcio-tools`, and the JS/TS generators into one OCI
image. Used by `codefly generate proto` and the
`codefly generate openAPI` pipeline.

## Build

There are two source-of-truth options today:

### Nix (preferred — reproducible, layered cache)

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

### Dockerfile (legacy — kept for parity until the Nix path is
default)

```sh
./scripts/build_companion.sh
```

The Dockerfile assembles the same set via apk + `go install`. Less
reproducible than Nix (apk packages vary across Alpine releases) but
needs no Linux builder VM. Will be retired once Phase 1's flake-based
build is the canonical path.

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

## Versioning

`info.codefly.yaml` carries the image version (`0.0.10` at time of
writing). Bump via `scripts/tag.sh` — same convention as every other
companion. Both the Dockerfile build and the Nix build read this
value so the tag stays consistent.
