#!/usr/bin/env bash
# combine-deps-plan — assemble every open Dependabot pull request onto one
# branch, and hand the result on as a bundle. Holds NO privileged credential.
#
# This is the half that touches unreviewed code. `refs/pull/<n>/head` is
# whatever was last pushed to that branch: filtering the pull requests by
# author establishes who OPENED each one, not who wrote the commits now on it.
# So this replays them, runs the repository's optional post-processing hook over
# the result, and stops — it cannot push, cannot open a pull request, and cannot
# close one, because the token it runs with is the built-in read-only one.
#
# combine-deps-publish.sh does those things, from the bundle, without ever
# checking the combined tree out.
#
# A repository with artefacts that must be regenerated after a bump (a lockfile
# in a second module, a checked-in hash manifest) puts that in
# .github/scripts/combine-deps-local.sh, which runs here — in the job with no
# credential, which is the only place running an assembled tree is safe —
# and anything it commits rides along in the bundle.
set -euo pipefail

BRANCH="deps/combined"
OUT="${1:?usage: combine-deps-plan.sh <output directory>}"
mkdir -p "$OUT"

BASE="$(gh repo view --json defaultBranchRef --jq .defaultBranchRef.name)"
git fetch --quiet origin "$BASE"

mapfile -t ROWS < <(
  gh pr list --state open --author "app/dependabot" --base "$BASE" \
    --limit 100 --json number,title --jq '.[] | [.number, .title] | @tsv' | sort -n
)

# The publish half reads this first. Absent or empty means there is nothing to
# push, which is a normal week, not a failure.
: > "$OUT/combined.tsv"
: > "$OUT/skipped.tsv"

if [ "${#ROWS[@]}" -eq 0 ]; then
  echo "combine-deps: no open Dependabot pull requests; nothing to combine."
  exit 0
fi

git config user.name "github-actions[bot]"
git config user.email "41898282+github-actions[bot]@users.noreply.github.com"
git checkout --quiet -B "$BRANCH" "origin/$BASE"

combined=()
skipped=()

for row in "${ROWS[@]}"; do
  number="${row%%$'\t'*}"
  title="${row#*$'\t'}"

  git fetch --quiet origin "refs/pull/${number}/head:refs/combine/${number}"
  base_point="$(git merge-base "origin/$BASE" "refs/combine/${number}")"

  if git cherry-pick --allow-empty "${base_point}..refs/combine/${number}" >/dev/null 2>&1; then
    combined+=("${number}"$'\t'"${title}")
    echo "combine-deps: replayed #${number} — ${title}"
  else
    git cherry-pick --abort >/dev/null 2>&1 || true
    skipped+=("${number}"$'\t'"${title}")
    echo "combine-deps: SKIPPED #${number} (conflicts with an earlier bump) — ${title}"
  fi
done

if [ "${#combined[@]}" -eq 0 ]; then
  echo "combine-deps: every pull request conflicted; leaving them all open."
  exit 0
fi

if [ -x .github/scripts/combine-deps-local.sh ]; then
  echo "combine-deps: running repository-local post-processing."
  .github/scripts/combine-deps-local.sh
fi

printf '%s\n' "${combined[@]}" > "$OUT/combined.tsv"
if [ "${#skipped[@]}" -gt 0 ]; then
  printf '%s\n' "${skipped[@]}" > "$OUT/skipped.tsv"
fi
echo "$BASE" > "$OUT/base"

{
  echo "Every open Dependabot pull request, replayed onto one branch by"
  echo "\`.github/workflows/combine-deps.yml\` so the week's dependency work is"
  echo "reviewed and merged once instead of per ecosystem."
  echo
  echo "### Combined"
  echo
  for row in "${combined[@]}"; do
    echo "- #${row%%$'\t'*} — ${row#*$'\t'}"
  done
  if [ "${#skipped[@]}" -gt 0 ]; then
    echo
    echo "### Left open (conflicted on replay, merge these by hand)"
    echo
    for row in "${skipped[@]}"; do
      echo "- #${row%%$'\t'*} — ${row#*$'\t'}"
    done
  fi
} > "$OUT/body.md"

# A thin bundle: the commits between the base and the combined branch, with
# origin/$BASE as the prerequisite the publish half already has. Carrying the
# work as data is what lets the credential-bearing job move it without ever
# checking it out.
git bundle create --quiet "$OUT/combined.bundle" "origin/${BASE}..${BRANCH}" "${BRANCH}"

echo "combine-deps: planned ${#combined[@]} pull request(s) into $OUT/combined.bundle."
