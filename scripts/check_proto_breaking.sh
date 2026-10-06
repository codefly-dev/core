#!/usr/bin/env bash
# The proto breaking-change gate.
#
# `buf breaking` answers one question — does this branch change the schema in a
# way a consumer notices — and it answers it well. What it cannot do is tell an
# accident from a decision, and this repository makes both: proto/ is the source
# of truth for the whole ecosystem, and it is also pre-customer, so a reviewed
# deletion is a normal thing to land. Before this script the gate had no way to
# record that, which left exactly two ways to land one: turn the job red on main
# and merge anyway, or add a permanent `ignore` to buf.yaml that goes on
# excusing everything in that file forever. Both lose the gate.
#
# So the break is declared where a reviewer already reads it: the commit that
# makes it. A commit touching proto/ whose subject carries the Conventional
# Commits `!` (`feat(runnable)!: ...`), or whose body carries a `BREAKING
# CHANGE:` footer, admits the findings of this run and nothing else. The
# declaration is per-commit, so it expires by itself — there is no file to rot,
# no entry to remove later, and a branch that breaks the schema without saying
# so is still red.
#
# What this does NOT do is check that the declared break is the break that
# happened. A branch declaring one deletion admits every finding of that run,
# which is why the findings are always printed in full, admitted or not: the
# reviewer reading `!` in the subject can see what it bought.
set -euo pipefail

remote="${BASE_REMOTE:-origin}"
url="$(git remote get-url "$remote" 2>/dev/null || true)"
case "$url" in
  *codefly-dev/core|*codefly-dev/core.git) ;;
  *)
    echo "BASE_REMOTE=$remote is not codefly-dev/core: its main is not the baseline the gate uses. Re-run with BASE_REMOTE=<remote>." >&2
    exit 1
    ;;
esac

git fetch "$remote" main
# The merge base, not the tip: CI checks out the pull request already merged
# into main, so a package main gained since the branch was cut is on both sides
# of that comparison. Against the tip it is missing from the branch alone and
# buf calls it a deletion — a break reported on a branch that touched no .proto.
baseline="$(git merge-base "$remote/main" HEAD)"

# Materialize only the baseline's schema. Asking buf to clone .git causes a
# partial-clone worktree's upload-pack to read missing promisor blobs with lazy
# fetching disabled. git archive runs in this repository, where Git can fetch
# its own missing objects, and also works when .git is a worktree pointer file.
baseline_dir="$(mktemp -d)"
trap 'rm -rf "$baseline_dir"' EXIT
git archive "$baseline" proto | tar -x -C "$baseline_dir"

findings=""
status=0
# buf resolves buf.lock deps from the BSR on every run, so anything other than
# 0 or 100 is buf failing rather than a verdict on the schema. Retry those.
for attempt in 1 2 3; do
  set +e
  findings="$(cd proto && buf breaking --against "$baseline_dir/proto" 2>&1)"
  status=$?
  set -e
  if [ "$status" -eq 0 ] || [ "$status" -eq 100 ]; then
    break
  fi
  echo "buf exited $status (attempt $attempt/3), not a rule violation; retrying in 15s" >&2
  [ "$attempt" -lt 3 ] && sleep 15
done

if [ "$status" -eq 0 ]; then
  echo "proto: no breaking change against ${remote}/main."
  exit 0
fi
if [ "$status" -ne 100 ]; then
  echo "$findings" >&2
  echo "buf breaking could not complete after 3 attempts" >&2
  exit 1
fi

echo "$findings"

declaring=""
while read -r commit; do
  [ -n "$commit" ] || continue
  # Only a commit that touched proto/ may declare a schema break: an unrelated
  # breaking change elsewhere in the branch is not a statement about the wire.
  git diff-tree --no-commit-id --name-only -r "$commit" -- proto | grep -q . || continue
  subject="$(git log -1 --format=%s "$commit")"
  body="$(git log -1 --format=%B "$commit")"
  # Conventional Commits: `type(scope)!: subject`, or a BREAKING CHANGE footer.
  if printf '%s' "$subject" | grep -Eq '^[a-z]+(\([^)]*\))?!:' \
    || printf '%s' "$body" | grep -Eq '^BREAKING[ -]CHANGE:'; then
    declaring="$commit"
    break
  fi
done <<EOF
$(git rev-list "${baseline}..HEAD")
EOF

if [ -n "$declaring" ]; then
  echo
  echo "proto: the findings above are admitted by a declared breaking change."
  echo "  $(git log -1 --format='%h %s' "$declaring")"
  echo "Core source, the regenerated bindings and every consumer move with it."
  exit 0
fi

echo
echo "proto: this branch breaks the schema and no commit declares it." >&2
echo "If the break is intended, say so on the commit that makes it — a Conventional" >&2
echo "Commits '!' (feat(runnable)!: ...) or a 'BREAKING CHANGE:' footer — and move the" >&2
echo "regenerated bindings and every consumer with it. If it is not intended, the" >&2
echo "findings above are the bug." >&2
exit 100
