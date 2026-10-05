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

# Into a ref, not into the working tree. `git fetch` from a bundle writes
# objects and moves a ref; it runs nothing, checks nothing out, and leaves HEAD
# where it is.
git fetch --quiet "$IN/combined.bundle" "+refs/heads/${BRANCH}:refs/heads/${BRANCH}"
git push --quiet --force-with-lease origin "refs/heads/${BRANCH}:refs/heads/${BRANCH}"

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
