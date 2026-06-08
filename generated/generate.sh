#!/usr/bin/env bash
# Regenerate Go bindings from the in-tree proto sources at core/proto.
#
# Source of truth is core/proto (NOT the published BSR module) — edit the
# .proto there and re-run this script in the same PR. Output lands in
# core/generated/go.
#
# Uses locally-installed protoc-gen-* plugins (deterministic, offline) and a
# goimports pass to match repo import grouping. Plugin versions are pinned so
# regeneration is byte-reproducible — bump them here in lockstep with go.mod.
set -euo pipefail

cd "$(dirname "$0")"

export PATH="$(go env GOPATH)/bin:$PATH"

# Pinned to match the versions the committed bindings were generated with.
PROTOC_GEN_GO=v1.36.11
PROTOC_GEN_GO_GRPC=v1.6.1
PROTOC_GEN_GRPC_GATEWAY=v2.29.0

need() { command -v "$1" >/dev/null 2>&1; }

echo "==> ensuring codegen plugins are installed"
need protoc-gen-go          || go install google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO}
need protoc-gen-go-grpc     || go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@${PROTOC_GEN_GO_GRPC}
need protoc-gen-grpc-gateway || go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@${PROTOC_GEN_GRPC_GATEWAY}
need goimports              || go install golang.org/x/tools/cmd/goimports@latest

echo "==> buf generate (Go) from ../proto"
buf generate ../proto --template buf.gen.local.yaml

echo "==> goimports"
goimports -w go

echo "==> done. Python bindings (for the CLI) generate via buf.gen.yaml where"
echo "    BSR remote plugins are reachable: buf generate ../proto --template buf.gen.yaml"
