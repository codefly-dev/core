# proto companion

The codefly proto companion bundles `buf`, `protoc`, `protoc-gen-*`,
`swagger`, `grpcio-tools`, and the JS/TS generators into one OCI
image. All proto generation runs through Codefly (`codefly generate proto`
and `codefly generate client`), which owns the pinned companion invocation.
Supported client languages are Go, Python, and TypeScript. The image and its
build contain no Rust toolchain or Rust generation plugins; unsupported
language requests fail before writing output.
The generation runner keeps the companion alive while executing its tools,
including Swagger client generation; it does not rely on the base image's default command.

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

The Dockerfile assembles the same set via apk + `go install`. The Go builder
runs on `BUILDPLATFORM` and cross-compiles static plugins for `TARGETARCH`;
it never compiles under ARM emulation. A separate target-platform Go image
supplies the runtime `go`/`gofmt`, so cross-building cannot accidentally ship
the build host's compiler. Upstream downloads were checked: Buf 1.73.0's
prebuilt binary carries Go 1.26.8 and x/net 0.58.0, including fixed findings,
and several other plugin downloads carry still older Go toolchains. Retaining
source builds preserves the current Go compiler instead of importing those
additional findings. Plugin dependency versions are unchanged by this build
speed fix; it does not claim a clean vulnerability scan of every plugin.

This path is less reproducible than Nix (apk packages vary across Alpine releases) but
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

`info.codefly.yaml` carries the image version. Version 0.0.21 removes Rust
generation and its build dependencies; 0.0.22 removes emulated Go compilation.
Publish through `codefly companion publish` using the [publication procedure](../../docs/runbooks/publish-companions.md);
changing the manifest alone does not publish an image. Core 0.19.14 selects
this companion.

## Python generation

Python bindings come from the companion's own plugin, `protoc-gen-codefly-python`
(`facades/python/protoc_gen_codefly_python.py`), as Go's come from
local plugins: it compiles the `*_pb2` messages and `*_pb2_grpc` stubs with the
`grpc_tools` pinned in the image's venv (`grpcio-tools==1.71.0`,
`protobuf==5.29.3`), so the gencode is versioned with the image. The Docker
runtime uses digest-pinned Python 3.13.18 on Alpine 3.24: these generator pins
have Python 3.13 wheels, and moving the generator to support a newer interpreter
would also change the generated-code runtime requirements. buf's remote
`protocolbuffers/python` and `grpc/python` plugins it replaces moved on their
own, and their gencode outran the protobuf runtime a generated library
declares. It ships in companion version 0.0.17.

The Python facade plugin (`facades/python/protoc_gen_codefly_facade_python.py`)
calls the gateway seam named for each RPC's shape: `unary`, `unary_stream`,
`stream_unary` or `stream_stream`. It ships in companion version 0.0.18. An
earlier `0.0.17` image renders every RPC as `unary`; 0.0.17 was republished
with the streaming plugin, and a machine that had pulled it before keeps the
old image.

A change to any of a companion's image inputs (its Dockerfile and every
build-context `COPY` source, `BuildSpec.Inputs`) must bump its
`info.codefly.yaml`: `internal/companionbump`, the required `Companion version`
check on every pull request, refuses one that does not.
