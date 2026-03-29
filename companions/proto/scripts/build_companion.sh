#!/usr/bin/env bash

BASE=$(basename $(dirname $(dirname "$0")))

YAML_FILE="companions/${BASE}/info.codefly.yaml"

if [ ! -f "$YAML_FILE" ]; then
    echo "Error: YAML file $YAML_FILE does not exist."
    exit 1
fi

CURRENT_VERSION=$(yq eval '.version' "$YAML_FILE")

# Build for the current platform (fast, for local dev)
# For multi-arch push, use: docker buildx build --platform linux/amd64,linux/arm64 --push
docker build --platform linux/$(uname -m | sed 's/x86_64/amd64/;s/aarch64/arm64/') \
  -f companions/${BASE}/Dockerfile \
  -t codeflydev/${BASE}:"$CURRENT_VERSION" .
