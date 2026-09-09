# Proto compilation companion for `codefly generate proto`.
#
# Size strategy:
#   1. Multi-stage: build all the Go plugins in a golang:1.27-alpine
#      builder stage, copy only the static binaries into the runtime.
#   2. Strip Go binaries with -ldflags="-s -w" during install — drops
#      DWARF + symbol tables, typically 20-30% smaller per binary.
#   3. Clean $GOMODCACHE + $GOCACHE after the last install — those
#      caches are scaffolding the runtime doesn't need.
#   4. Runtime FROM the codefly companion base instead of golang:1.26-
#      alpine — saves ~170 MB of Go stdlib we don't need since the
#      binaries are statically compiled. Still need apk-installed
#      nodejs+npm for TypeScript generation.

# The codefly base image is a build argument: this repository declares which
# companion image it builds on, and the builder resolves that to a registry.
# The default keeps a plain `docker build` working and is pinned to the version
# in companions/codefly/info.codefly.yaml.
ARG CODEFLY_BASE_IMAGE=ghcr.io/codefly-dev/codefly:0.0.4

# --- Builder stage: compile every Go plugin from source --------------

FROM golang:1.27-alpine AS builder

RUN apk add --no-cache git python3 py3-pip nodejs npm protobuf

# Install the Go-based plugins, stripped, in one RUN so the cache
# cleanup at the end actually takes effect in the final image layer.
# Each binary is copied out individually in the runtime stage; the
# module cache is not.
RUN go install -ldflags="-s -w" github.com/bufbuild/buf/cmd/buf@v1.71.0 && \
    go install -ldflags="-s -w" google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11 && \
    go install -ldflags="-s -w" google.golang.org/grpc/cmd/protoc-gen-go-grpc@v1.6.1 && \
    go install -ldflags="-s -w" github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@v2.29.0 && \
    go install -ldflags="-s -w" github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@v2.29.0 && \
    go install -ldflags="-s -w" connectrpc.com/connect/cmd/protoc-gen-connect-go@v1.20.0 && \
    go install -ldflags="-s -w" github.com/go-swagger/go-swagger/cmd/swagger@v0.35.3 && \
    go install -ldflags="-s -w" github.com/envoyproxy/protoc-gen-validate@v1.2.1

# Go facade plugin (protoc-gen-codefly-facade-go), built from in-tree source.
# The build context is companions/proto, so facades/go is available here.
COPY facades/go /src/facade-go
RUN cd /src/facade-go && \
    go build -ldflags="-s -w" -o /go/bin/protoc-gen-codefly-facade-go .

# Python grpcio-tools (for grpc_* helpers) — built in a separate venv
# so site-packages doesn't collide with the system Python.
RUN python3 -m venv /venv && /venv/bin/pip install --no-cache-dir grpcio-tools

# --- Runtime stage: minimal alpine + the codefly base ---------------

FROM ${CODEFLY_BASE_IMAGE}

# Runtime dependencies:
#   - nodejs + npm: for the TypeScript generators below (unavoidable).
#   - protobuf: provides protoc + its shared libraries (libprotoc.so,
#     libabsl_*). `protoc` is dynamically linked; COPYing just the
#     binary from the builder without its .so neighbors produces
#     "symbol not found" at first invocation.
#   - go: required at runtime by `swagger generate client` to format
#     generated source. Without it `gofmt` is unreachable and swagger
#     bails with "go command required, not found". The Go-built plugins
#     (buf, protoc-gen-*) are static so they don't need this — only
#     swagger does, but it's the single tool that breaks the openapi
#     pipeline if missing. Adds ~150 MB; unavoidable.
#   - Go-built plugins (buf, protoc-gen-*) ARE static (CGO_ENABLED=0
#     by default for `go install`), so we COPY just those binaries
#     without bringing the Go runtime.
# HOME=/tmp so the companion works when run as an arbitrary non-root UID
# (codefly runs it as the host user on Linux CI). buf and npm/npx write
# their caches under $HOME; the default /root isn't writable by that user.
ENV HOME=/tmp

# python3 + a dedicated /venv host the Python facade + strip plugins. The venv
# is built here (not copied from the builder) so it is bound to this stage's
# python — the builder's alpine ships a different python minor version, and a
# venv is not portable across them.
RUN apk add --no-cache nodejs npm protobuf go python3 py3-pip && \
    python3 -m venv /venv && \
    /venv/bin/pip install --no-cache-dir protobuf && \
    npm install -g \
        openapi-typescript@7.13.0 \
        swagger2openapi@7.0.8 \
        @bufbuild/protoc-gen-es@2.11.0 && \
    npm cache clean --force

COPY --from=builder /go/bin/buf /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-go /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-go-grpc /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-grpc-gateway /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-openapiv2 /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-validate /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-connect-go /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-codefly-facade-go /usr/local/bin/
COPY --from=builder /go/bin/swagger /usr/local/bin/
COPY --from=builder /usr/bin/grpc_* /usr/local/bin/

# Facade plugin sources (Python scripts + the TS npm package).
COPY facades/python /opt/codefly-facades/python
COPY facades/ts /opt/codefly-facades/ts

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

# Python facade plugin + descriptor-set option stripper. Both facade plugin
# names are exposed (protoc-gen-solution_facade is kept for one release so the
# runtime repo can switch), each a wrapper running the copied venv's python.
RUN printf '#!/bin/sh\nexec /venv/bin/python /opt/codefly-facades/python/protoc_gen_codefly_facade_python.py "$@"\n' \
        > /usr/local/bin/protoc-gen-codefly-facade-python && \
    printf '#!/bin/sh\nexec /venv/bin/python /opt/codefly-facades/python/strip_custom_options.py "$@"\n' \
        > /usr/local/bin/codefly-proto-strip-options && \
    chmod +x /usr/local/bin/protoc-gen-codefly-facade-python /usr/local/bin/codefly-proto-strip-options && \
    ln -sf protoc-gen-codefly-facade-python /usr/local/bin/protoc-gen-solution_facade

WORKDIR /workspace
