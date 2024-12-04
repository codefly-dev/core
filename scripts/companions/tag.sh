#!/bin/bash

# Get the directory of the current script
SCRIPT_DIR=$(dirname "$0")

YAML_FILE="$SCRIPT_DIR/../info.codefly.yaml"

if [ ! -f "$YAML_FILE" ]; then
    echo "Error: YAML file $YAML_FILE does not exist."
    exit 1
fi

# Argument is patch/minor/major and defaults to patch
NEW_VERSION_TYPE=${1:-patch}

CURRENT_VERSION=$(yq eval '.version' "$YAML_FILE")
NEW_VERSION=$(semver bump "$NEW_VERSION_TYPE" "$CURRENT_VERSION")

echo "Companion: $SCRIPT_DIR" "Current version: $CURRENT_VERSION -> New version: $NEW_VERSION"


# Update the version in the YAML file (for macOS)
sed -i '' "s/version:.*/version: $NEW_VERSION/" "$YAML_FILE"
