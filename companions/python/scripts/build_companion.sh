#!/usr/bin/env bash

BASE=$(basename $(dirname $(dirname "$0")))

YAML_FILE="companions/${BASE}/info.codefly.yaml"

if [ ! -f "$YAML_FILE" ]; then
    echo "Error: YAML file $YAML_FILE does not exist."
    exit 1
fi

CURRENT_VERSION=$(yq eval '.version' "$YAML_FILE")

docker build -f companions/${BASE}/Dockerfile -t codeflydev/${BASE}:"$CURRENT_VERSION" .
