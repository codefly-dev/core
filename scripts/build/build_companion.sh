#!/usr/bin/env bash

YAML_FILE="version/info.codefly.yaml"

if [ ! -f "$YAML_FILE" ]; then
    echo "Error: YAML file $YAML_FILE does not exist."
    exit 1
fi

CURRENT_VERSION=$(yq eval '.version' "$YAML_FILE")

docker build -f generate/companion/Dockerfile -t codeflydev/companion:"$CURRENT_VERSION" .
