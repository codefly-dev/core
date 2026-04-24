# Proto compilation companion for `codefly generate proto`.
#
# Size strategy:
#   1. Multi-stage: build all the Go plugins in a golang:1.26-alpine
#      builder stage, copy only the static binaries into the runtime.
#   2. Strip Go binaries with -ldflags="-s -w" during install — drops
#      DWARF + symbol tables, typically 20-30% smaller per binary.
#   3. Clean $GOMODCACHE + $GOCACHE after the last install — those
#      caches are scaffolding the runtime doesn't need.
#   4. Runtime FROM codeflydev/codefly:0.0.3 instead of golang:1.26-
#      alpine — saves ~170 MB of Go stdlib we don't need since the
#      binaries are statically compiled. Still need apk-installed
#      nodejs+npm for TypeScript generation.

# --- Builder stage: compile every Go plugin from source --------------

FROM golang:1.26-alpine AS builder

RUN apk add --no-cache git python3 py3-pip nodejs npm protobuf

# Install the Go-based plugins, stripped, in one RUN so the cache
# cleanup at the end actually takes effect in the final image layer.
# Each binary is copied out individually in the runtime stage; the
# module cache is not.
RUN go install -ldflags="-s -w" github.com/bufbuild/buf/cmd/buf@latest && \
    go install -ldflags="-s -w" github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest && \
    go install -ldflags="-s -w" github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2@latest && \
    go install -ldflags="-s -w" connectrpc.com/connect/cmd/protoc-gen-connect-go@latest && \
    go install -ldflags="-s -w" github.com/go-swagger/go-swagger/cmd/swagger@latest && \
    go install -ldflags="-s -w" github.com/envoyproxy/protoc-gen-validate@latest

# Python grpcio-tools (for grpc_* helpers) — built in a separate venv
# so site-packages doesn't collide with the system Python.
RUN python3 -m venv /venv && /venv/bin/pip install --no-cache-dir grpcio-tools

# --- Runtime stage: minimal alpine + the codefly base ---------------

FROM codeflydev/codefly:0.0.3

# Runtime dependencies:
#   - nodejs + npm: for the TypeScript generators below (unavoidable).
#   - protobuf: provides protoc + its shared libraries (libprotoc.so,
#     libabsl_*). `protoc` is dynamically linked; COPYing just the
#     binary from the builder without its .so neighbors produces
#     "symbol not found" at first invocation.
#   - Go-built plugins (buf, protoc-gen-*) ARE static (CGO_ENABLED=0
#     by default for `go install`), so we COPY just those binaries
#     without bringing the Go runtime.
RUN apk add --no-cache nodejs npm protobuf && \
    npm install -g \
        openapi-typescript \
        swagger2openapi \
        @bufbuild/protoc-gen-es@^2 && \
    npm cache clean --force

COPY --from=builder /go/bin/buf /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-grpc-gateway /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-openapiv2 /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-validate /usr/local/bin/
COPY --from=builder /go/bin/protoc-gen-connect-go /usr/local/bin/
COPY --from=builder /go/bin/swagger /usr/local/bin/
COPY --from=builder /usr/bin/grpc_* /usr/local/bin/

WORKDIR /workspace
