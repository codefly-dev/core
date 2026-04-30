#!/bin/bash
# Cut a new core release.
#
# Bumps version/info.codefly.yaml, commits, tags, and pushes both
# the commit and the tag to origin. NEVER force-pushes — every
# pre-flight check is upfront so a divergent state aborts cleanly
# rather than overwriting it.
#
# Usage:
#   ./scripts/publish/tag.sh           # patch bump (default)
#   ./scripts/publish/tag.sh patch
#   ./scripts/publish/tag.sh minor
#   ./scripts/publish/tag.sh major
set -euo pipefail

YAML_FILE="version/info.codefly.yaml"
NEW_VERSION_TYPE=${1:-patch}

if [ ! -f "$YAML_FILE" ]; then
    echo "Error: $YAML_FILE does not exist." >&2
    exit 1
fi

# --- Pre-flight: working tree clean ---
if ! git diff-index --quiet HEAD --; then
  echo "Error: working tree has uncommitted changes. Commit or stash first." >&2
  git status --short >&2
  exit 1
fi

# --- Pre-flight: on main ---
BRANCH=$(git rev-parse --abbrev-ref HEAD)
if [ "$BRANCH" != "main" ]; then
  echo "Error: not on main (on '$BRANCH'). Releases tag from main only." >&2
  exit 1
fi

# --- Pre-flight: synced with remote main ---
# Fail loud rather than silently overwrite a teammate's push.
git fetch origin main --quiet
LOCAL=$(git rev-parse HEAD)
REMOTE=$(git rev-parse origin/main)
if [ "$LOCAL" != "$REMOTE" ]; then
  echo "Error: local main ($LOCAL) diverged from origin/main ($REMOTE)." >&2
  echo "Pull or push first; refusing to overwrite." >&2
  exit 1
fi

# --- Compute version ---
CURRENT_VERSION=$(yq eval '.version' "$YAML_FILE")
NEW_VERSION=$(semver bump "$NEW_VERSION_TYPE" "$CURRENT_VERSION")
NEW_TAG="v$NEW_VERSION"

# --- Pre-flight: tag doesn't already exist (local + remote) ---
# Tags are intended to be immutable. If $NEW_TAG already exists
# anywhere, the right move is a different bump — never overwrite.
if git rev-parse "$NEW_TAG" >/dev/null 2>&1; then
  echo "Error: tag $NEW_TAG already exists locally. Delete it or pick a different bump." >&2
  exit 1
fi
if git ls-remote --tags origin "refs/tags/$NEW_TAG" 2>/dev/null | grep -q .; then
  echo "Error: tag $NEW_TAG already exists on origin. Pick a different bump." >&2
  exit 1
fi

# --- Bump, commit, tag ---
sed -i '' "s/version:.*/version: $NEW_VERSION/" "$YAML_FILE"
git add "$YAML_FILE"
git commit -m "release: $NEW_TAG"
git tag -a "$NEW_TAG" -m "Version $NEW_VERSION"

# --- Push (no force on either) ---
git push origin main
git push origin "$NEW_TAG"

echo "Released $NEW_TAG"
