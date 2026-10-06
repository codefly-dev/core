#!/usr/bin/env bash
# combine-deps-publish — push the branch combine-deps-plan.sh assembled and
# open the combined pull request. Holds the credential; runs none of the tree.
#
# The split is the point. The commits arrive as a bundle and are moved into a
# ref with `git fetch` and `git push`; the working tree stays on the default
# branch throughout, so nothing from a Dependabot branch — or from whatever
# else was pushed to one — ever executes beside this token.
#
# One caveat before you close the combined pull request: closing a Dependabot
# pull request tells Dependabot not to re-open one for that same version.
# Merging moves the dependency, so the next pass proposes the next version and
# nothing is lost. Abandoning it instead forfeits that batch until a newer
# version ships.
set -euo pipefail

BRANCH="deps/combined"
IN="${1:?usage: combine-deps-publish.sh <input directory>}"

if [ ! -s "$IN/combined.tsv" ]; then
  echo "combine-deps: the plan combined nothing; nothing to publish."
  exit 0
fi

BASE="$(cat "$IN/base")"
mapfile -t combined < "$IN/combined.tsv"

# Every number this half will act on must be a bare number of THIS repository.
# The artifact is written by a job that handles unreviewed code, so a value from
# it is checked before this token is pointed at anything.
for row in "${combined[@]}"; do
  number="${row%%$'\t'*}"
  if ! printf '%s' "$number" | grep -Eq '^[0-9]+$'; then
    echo "::error::refusing to act on <${number}>: a pull request is named by a bare number here." >&2
    exit 1
  fi
done

# And the bundle may carry nothing but dependency manifests. The plan replays
# Dependabot commits; anything else in them is not a dependency bump.
git fetch --quiet "$IN/combined.bundle" "+refs/heads/${BRANCH}:refs/heads/${BRANCH}"
mapfile -t touched < <(git diff --name-only "origin/${BASE}...refs/heads/${BRANCH}")
for path in "${touched[@]}"; do
  case "$path" in
    go.mod|go.sum|*/go.mod|*/go.sum|package.json|*/package.json|\
    pnpm-lock.yaml|*/pnpm-lock.yaml|package-lock.json|*/package-lock.json|\
    .github/workflows/*.yml|requirements*.txt|*/requirements*.txt|Dockerfile|*/Dockerfile) ;;
    *)
      echo "::error::refusing to publish: the combined branch changes ${path}, which is not a dependency manifest." >&2
      exit 1
      ;;
  esac
done

# The ref is already in place from the inspection above; nothing was checked
# out. `git fetch` from a bundle writes objects and moves a ref, leaving HEAD
# where it is.
git push --quiet --force-with-lease origin "refs/heads/${BRANCH}:refs/heads/${BRANCH}"

# Recomputed here, deliberately. Reading it from the artifact meant the
# unprivileged half chose what this half acts on, and a planted value -- a full
# URL rather than a number -- would point this organisation's token at a pull
# request in another repository.
existing="$(gh pr list --state open --head "$BRANCH" --json number --jq '.[0].number // empty')"
if [ -n "$existing" ]; then
  gh pr edit "$existing" --body-file "$IN/body.md"
  combined_pr="$existing"
else
  gh pr create --base "$BASE" --head "$BRANCH" \
    --title "build(deps): combined dependency updates" --body-file "$IN/body.md"
  combined_pr="$(gh pr list --state open --head "$BRANCH" --json number --jq '.[0].number')"
fi

for row in "${combined[@]}"; do
  number="${row%%$'\t'*}"
  gh pr comment "$number" --body "Superseded by #${combined_pr}, which carries this bump together with the rest of the week's dependency updates."
  gh pr close "$number"
done

echo "combine-deps: combined ${#combined[@]} pull request(s) into #${combined_pr}."
