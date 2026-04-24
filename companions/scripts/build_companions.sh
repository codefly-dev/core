#!/usr/bin/env bash
# Build every codefly-owned companion Docker image.
# Run from core/: ./companions/scripts/build_companions.sh
#
# Build order matters: the `codefly` base image must be built first so
# language companions can COPY --from=codeflydev/codefly their CLI.
# Pinning is enforced in the individual Dockerfiles — no :latest anywhere.
#
# Produces:
#   codeflydev/codefly:<v>  — alpine base + codefly CLI + common tools
#   codeflydev/go:<v>       — Go plugin runtime (golang:1.26-alpine + gopls)
#   codeflydev/python:<v>   — Python plugin runtime (python:3.13.1-alpine3.21 + uv)
#   codeflydev/node:<v>     — Node plugin runtime (node:22.12.0-alpine3.21)
#   codeflydev/proto:<v>    — proto/buf companion for `codefly generate proto`
set -e
cd "$(dirname "$0")/../.."

# Prerequisite: cross-compile the codefly CLI for linux/amd64, stripped.
# Every language companion COPYs this binary from core/bin/linux/codefly.
# -s -w drops the symbol table + DWARF debug info; typical 20-30% binary
# shrink with zero runtime cost. Saves ~14MB per image (5 images × 14 =
# ~70MB total across the companion set).
echo "==> Cross-compiling codefly for linux/amd64 (stripped)"
(cd ../cli && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags '-s -w -extldflags "-static"' \
    -o ../core/bin/linux/codefly .)

# Base image first — other companions COPY --from this one.
./companions/codefly/scripts/build_companion.sh

# Language runtime companions.
./companions/go/scripts/build_companion.sh
./companions/python/scripts/build_companion.sh
./companions/node/scripts/build_companion.sh

# Dev-tooling companions (not plugin runtimes).
./companions/proto/scripts/build_companion.sh
