#!/usr/bin/env bash
set -euo pipefail

# Guard: version/info.codefly.yaml must stay in step with the published tags.
#
# version.Version() reads this file — it is embedded, and despite what
# version/doc.go used to claim there is no -ldflags override, so the file is
# authoritative in every build. composition/contracts.go gates every package
# install on it: a file reading 0.3.20 in a binary built from 0.3.24 code
# rejects any package declaring `minimum-codefly-version: >0.3.20` with
# "Codefly 0.3.20 does not satisfy package requirement". Nothing else in CI
# read this file, so it has silently rotted before — it sat at 0.3.8 while
# v0.3.18 shipped, and at 0.3.20 while v0.3.24 shipped.
#
# The file may run AHEAD of the tags: that is a release-prep bump, and
# .github/workflows/version-tag.yml cuts the tag from it once CI is green.
# Only "behind" is a bug.
#
# Run from the repository root: make check-version-tag

file="version/info.codefly.yaml"

declared=$(sed -n 's/^version:[[:space:]]*//p' "$file" | tr -d '[:space:]')
if [ -z "$declared" ]; then
  echo "$file has no version" >&2
  exit 1
fi
# resources/agent.go calls this through shared.Must inside init(), so an
# unparseable version panics every binary in the ecosystem at process start
# rather than failing anywhere useful. Reject it here. version/version_test.go
# enforces the same shape, so `go test` catches it too.
if ! printf '%s' "$declared" | grep -Eq '^[0-9]+\.[0-9]+\.[0-9]+$'; then
  echo "$file: version <$declared> is not a bare semver (x.y.z)" >&2
  exit 1
fi

# Tag NAMES read off the remote: works with CI's shallow checkout and needs no
# fetch. Annotated tags yield two lines (the tag object and the dereferenced
# commit); resolve to the commit so a tag's identity is the commit it names.
remote_tags() {
  git ls-remote --tags origin 'v*' 2>/dev/null || true
}

resolved=$(remote_tags | awk '
  /\^\{\}$/ { sub(/\^\{\}$/, "", $2); commit[$2] = $1; next }
              { if (!($2 in commit)) commit[$2] = $1 }
  END { for (ref in commit) print commit[ref], ref }
' | sed 's|refs/tags/||' | grep -E ' v[0-9]+\.[0-9]+\.[0-9]+$' || true)

if [ -z "$resolved" ]; then
  # Offline or no remote: fall back to local tags. Still better than no check.
  resolved=$(git tag --list 'v*' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' \
    | while read -r tag; do echo "$(git rev-list -n1 "$tag") $tag"; done || true)
fi

if [ -z "$resolved" ]; then
  echo "no published version tags found — nothing to compare against"
  exit 0
fi

# --- one commit, one version ------------------------------------------------
#
# Three commits already carry two version tags each. That is not cosmetic:
# `git describe --tags` on 80611ae reports v0.3.21, not v0.3.24, so any release
# step deriving the next version from describe walks BACKWARDS and re-cuts an
# already-published version. These three are published and cached by the Go
# module proxy — deleting a tag does not un-publish it, so unwinding them is a
# release decision, not something this script can do. They are frozen here as a
# baseline so every NEW duplicate fails.
baseline_duplicate() {
  case "$1" in
    80611aedc191d6fd5ec2f7b78709a1fad4db7de2) return 0 ;;  # v0.3.21 + v0.3.24
    f607a0a7324689dc55b10c742939f258748a084a) return 0 ;;  # v0.3.16 + v0.3.17
    d65ada600f9febd11e7c63d489c35712439c9a0d) return 0 ;;  # v0.0.9  + v0.0.11
    *) return 1 ;;
  esac
}

failed=0
for sha in $(printf '%s\n' "$resolved" | awk '{print $1}' | sort | uniq -d); do
  tags=$(printf '%s\n' "$resolved" | awk -v s="$sha" '$1 == s {print $2}' | sort -V | tr '\n' ' ')
  if baseline_duplicate "$sha"; then
    echo "note: ${sha:0:8} carries $tags (known, pre-existing)"
    continue
  fi
  echo "commit $sha carries more than one version tag: $tags" >&2
  failed=1
done
if [ "$failed" -ne 0 ]; then
  echo "Two versions on one commit make \`git describe\` ambiguous and can walk the" >&2
  echo "version backwards. Keep the higher tag; do not reuse the lower one." >&2
  exit 1
fi

# --- the file must not lag the tags -----------------------------------------
#
# Deliberately max-of-tags, not `git describe`, for the reason above.
latest=$(printf '%s\n' "$resolved" | awk '{print $2}' | sed 's/^v//' | sort -V | tail -1)

behind=$(printf '%s\n%s\n' "$declared" "$latest" | sort -V | head -1)
if [ "$declared" != "$latest" ] && [ "$behind" = "$declared" ]; then
  cat >&2 <<MSG
$file declares $declared but v$latest is published.

A binary built from this tree reports $declared, so every package requiring
more than $declared is rejected at install with ErrContract. Set the version
in $file to $latest, or to the next version you are about to release.
MSG
  exit 1
fi

echo "$file declares $declared; latest published tag is v$latest — ok"
