#!/usr/bin/env bash
# Build all companion Docker images used by LSP (go) and proto generation.
# Run from core/: ./companions/scripts/build_companions.sh
# Produces: codeflydev/go:<version>, codeflydev/proto:<version>
set -e
cd "$(dirname "$0")/../.."

./companions/go/scripts/build_companion.sh
./companions/proto/scripts/build_companion.sh
