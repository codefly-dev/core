#!/usr/bin/env bash

# Check if an argument is provided
if [ $# -eq 0 ]; then
    echo "Error: Please provide the companion name as an argument."
    echo "Usage: $0 <companion_name>"
    exit 1
fi

# Set the BASE variable to the provided argument
BASE="$1"

YAML_FILE="companions/${BASE}/info.codefly.yaml"

if [ ! -f "$YAML_FILE" ]; then
    echo "Error: YAML file $YAML_FILE does not exist."
    exit 1
fi

CURRENT_VERSION=$(yq eval '.version' "$YAML_FILE")

docker build -f companions/${BASE}/Dockerfile -t codeflydev/${BASE}:"$CURRENT_VERSION" -t codeflydev/${BASE}:latest .
