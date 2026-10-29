# Proto compilation companion for `codefly generate proto`.
#
# Compile pinned Go tools on the build host and cross-compile static binaries
# for the target. Target-platform emulation is only needed by runtime package
# installation, never by the Go compiler. Upstream prebuilt plugins often carry
# older Go runtimes and dependencies, so downloading them is not equivalent.

# The codefly base image is a build argument: this repository declares which
# companion image it builds on, and the builder resolves that to a registry.
# The default keeps a plain `docker build` working and is pinned to the version
# in companions/codefly/info.codefly.yaml.
ARG CODEFLY_BASE_IMAGE=ghcr.io/codefly-dev/codefly:0.0.7
FROM ${CODEFLY_BASE_IMAGE} AS codeflybase

# The runtime needs the TARGET architecture's Go distribution for goimports
# and Swagger. This stage only supplies files; it executes no target compiler.
FROM golang:1.27-alpine AS toolchain

# --- Native builder: cross-compile the pinned plugins ----------------
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS builder
ARG TARGETARCH
ENV GOOS=linux GOARCH=${TARGETARCH} CGO_ENABLED=0
RUN apk add --no-cache git

# Install stripped plugins in one layer. Only their binaries are copied
# into the runtime; the source and build caches remain in this stage.
RUN go install -ldflags="-s -w" github.com/bufbuild/buf/cmd/buf@v1.73.0 && \
    go install -ldflags="-s -w" google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11 && \
    go install -ldflags="-s -w" google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1 && \
    go install -ldflags="-s -w" github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.29.0 && \
    go install -ldflags="-s -w" github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@v2.29.0 && \
    go install -ldflags="-s -w" connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.20.0 && \
    go install -ldflags="-s -w" github.com/go-swagger/go-swagger/cmd/swagger@v0.35.3 && \
    go install -ldflags="-s -w" github.com/envoyproxy/protoc-gen-validate@v1.2.1 && \
    go install -ldflags="-s -w" golang.org/x/tools/cmd/goimports@v0.50.0

# go install puts cross-compiled commands in $GOPATH/bin/linux_<arch>.
# Normalize the output location for the runtime COPY instructions below.
RUN if [ "$TARGETARCH" != "$(go env GOHOSTARCH)" ]; then \
        mv /go/bin/linux_"$TARGETARCH"/* /go/bin/; \
    fi

# Go facade plugin (protoc-gen-codefly-facade-go), built from in-tree source.
COPY companions/proto/facades/go /src/facade-go
RUN cd /src/facade-go && \
    go build -ldflags="-s -w" -o /go/bin/protoc-gen-codefly-facade-go .

# --- Runtime stage: minimal alpine + the codefly base ---------------

# Keep the interpreter compatible with the pinned generator's wheels and
# gencode contract. Alpine's python3 moves independently; grpcio-tools 1.71
# has Python 3.13 wheels, not 3.14 wheels. As in the Python companion, copy
# the freshly built CLI into a language-specific runtime base.
FROM python:3.13.18-alpine3.24@sha256:bbc0290146ebe0a8b9a8ed07d3f2c7bdeb4d917fc93300a4e47490c8b5a11b48
COPY --from=codeflybase /bin/codefly /bin/codefly

# Runtime dependencies:
#   - nodejs + npm: for the TypeScript generators below (unavoidable).
#   - protobuf: provides protoc + its shared libraries (libprotoc.so,
#     libabsl_*). `protoc` is dynamically linked; COPYing just the
#     binary from the builder without its .so neighbors produces
#     "symbol not found" at first invocation.
#   - go: required at runtime by `swagger generate client` to format
#     generated source, and by goimports to name a package: it runs
#     `go list` in the consumer's module, and refuses a module whose
#     go.mod requires a newer Go than it is ("requires go >= 1.27.0
#     (running go 1.23.9; GOTOOLCHAIN=local)"). It then falls back to
#     syntax-only formatting and silently emits no alias where the
#     package name is not the path's last element — which is the
#     committed shape, so the companion's output drifts from every
#     consumer by exactly those lines. The apk go is whatever the base
#     alpine ships (1.23 on 3.21); the toolchain is COPYed from the
#     target toolchain below instead, so the runtime Go is the ecosystem's Go
#     by construction. The Go-built plugins (buf, protoc-gen-*) are
#     static and never need it. Adds ~270 MB; unavoidable.
#   - Go-built plugins (buf, protoc-gen-*) ARE static (CGO_ENABLED=0
#     explicitly in the builder), so we COPY just those binaries
#     without bringing the Go runtime.
# python3 + a dedicated /venv host the Python facade + strip plugins. The venv
# is built here (not copied from the builder) so it is bound to this stage's
# Python 3.13 — the builder may ship a different Python minor version, and a
# venv is not portable across them.
RUN apk add --no-cache bash ca-certificates curl git jq procps tzdata nodejs npm protobuf && \
    python3 -m venv /venv && \
    /venv/bin/pip install --no-cache-dir --only-binary=:all: protobuf==5.29.3 grpcio-tools==1.71.0 && \
    npm install -g \
        openapi-typescript@7.13.0 \
        swagger2openapi@7.0.8 \
        @bufbuild/protoc-gen-es@2.11.0 && \
    npm cache clean --force

# The target toolchain, not apk's: goimports and swagger both shell out
# to `go`, and a Go older than what consumers' go.mod files require makes
# goimports degrade silently (see the runtime-dependencies note above).
COPY --from=toolchain /usr/local/go /usr/local/go
ENV PATH=/usr/local/go/bin:$PATH
COPY --from=builder /go/bin/buf /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-go /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-go-grpc /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-grpc-gateway /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-openapiv2 /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-validate /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-connect-go /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-codefly-facade-go /usr/local/bin/
COPY --from=builder /go/bin/swagger /usr/local/bin/
# goimports is part of generation, not a consumer's afterthought: every
# consumer commits its Go bindings in goimports shape and gates on it, so the
# companion formats what buf wrote before it hands the tree back. Pinned like
# the plugins — a formatter that moves with @latest moves the committed shape.
COPY --from=builder /go/bin/goimports /usr/local/bin/

# Facade plugin sources (Python scripts + the TS npm package).
COPY companions/proto/facades/python /opt/codefly-facades/python
COPY companions/proto/facades/ts /opt/codefly-facades/ts

# TypeScript facade plugin — protoc-gen-codefly-facade-ts. Its one dependency
# (@bufbuild/protoplugin, pinned via package-lock.json) is installed in place;
# the executable entry is symlinked onto PATH so buf finds it. `npm install -g`
# on a local dir only symlinks back here without installing deps, so it can't
# be used.
RUN cd /opt/codefly-facades/ts && \
    npm ci --omit=dev --no-audit --no-fund && \
    npm cache clean --force && \
    chmod +x protoc-gen-codefly-facade-ts.js && \
    ln -sf /opt/codefly-facades/ts/protoc-gen-codefly-facade-ts.js \
        /usr/local/bin/protoc-gen-codefly-facade-ts

# Python bindings plugin (protoc-gen-codefly-python: the venv's pinned grpc_tools
# compiling the *_pb2 and *_pb2_grpc files), the facade plugin and the
# descriptor-set option stripper. Both facade plugin names are exposed
# (protoc-gen-solution_facade is kept for one release so the runtime repo can
# switch), each a wrapper running the copied venv's python.
RUN printf '#!/bin/sh\nexec /venv/bin/python /opt/codefly-facades/python/protoc_gen_codefly_python.py "$@"\n' \
        > /usr/local/bin/protoc-gen-codefly-python && \
    printf '#!/bin/sh\nexec /venv/bin/python /opt/codefly-facades/python/protoc_gen_codefly_facade_python.py "$@"\n' \
        > /usr/local/bin/protoc-gen-codefly-facade-python && \
    printf '#!/bin/sh\nexec /venv/bin/python /opt/codefly-facades/python/strip_custom_options.py "$@"\n' \
        > /usr/local/bin/codefly-proto-strip-options && \
    chmod +x /usr/local/bin/protoc-gen-codefly-python /usr/local/bin/protoc-gen-codefly-facade-python /usr/local/bin/codefly-proto-strip-options && \
    ln -sf protoc-gen-codefly-facade-python /usr/local/bin/protoc-gen-solution_facade

# Exercise target executables in both architecture builds. A successful native
# compile alone would not catch copying the host Go distribution into runtime.
# Keep installation caches under /root, then use the runtime home as a non-root
# UID. The temporary mount prevents smoke checks from baking owned caches into
# /tmp, where a different host UID must create its own Buf/Go/npm caches.
ENV HOME=/tmp
USER 65532:65532
ARG TARGETARCH
RUN --mount=type=tmpfs,target=/tmp \
    test "$(go env GOARCH)" = "$TARGETARCH" && \
    buf --version && \
    mkdir /tmp/proto-check && \
    printf 'syntax = "proto3";\npackage cachecheck;\nmessage Check {}\n' > /tmp/proto-check/check.proto && \
    buf build /tmp/proto-check --output /tmp/proto-check.bin && \
    protoc-gen-go --version && \
    protoc-gen-go-grpc --version && \
    swagger version && \
    npm cache verify

USER 0:0
WORKDIR /workspace
