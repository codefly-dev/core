#!/usr/bin/env bash
# Enforces the two claims docs/reliability-audit-2026-09-09.md makes that rot
# silently.
#
# That document is normative for 34 issues across six repositories, so a reader
# acts on it. Two kinds of claim in it go stale without anything failing:
#
#   1. Go symbols it names as the seam a finding sits on. The open fixes change
#      exactly those symbols, so a merged fix can leave the document asserting a
#      defect against a function that no longer exists. Checked offline.
#   2. Issue links and their finding codes. Issues get closed as duplicates,
#      split, or re-scoped, and the finding -> issue map then points somewhere
#      wrong while still looking authoritative. Checked only when `gh` can reach
#      the API; skipped cleanly otherwise so the guard is never flaky.
#
# This is the enforcement half of the doc+guard pattern core already uses for
# the CGO-free surface (scripts/check_cgo_free.sh, docs/cgo.md): the claim fails
# in the PR that breaks it, not months later in a reader's head.

set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

DOC="docs/reliability-audit-2026-09-09.md"
[[ -f "$DOC" ]] || { echo "check_audit_doc: $DOC not found"; exit 1; }

fail=0

# --- 1. Code anchors -------------------------------------------------------
# Top-level funcs the document names as current-at-snapshot code seams, and the
# file it attributes each to. Keep in sync with the document; removing a symbol here
# because a fix renamed it means updating the prose that names it too.
ANCHORS=(
	"cliServerAddress:sdk/dependencies.go"
	"attachDependencies:sdk/dependencies.go"
	"WithNamingScope:sdk/dependencies.go"
	"WithFixture:sdk/dependencies.go"
	"WithRunProfile:sdk/dependencies.go"
	"WithDependencies:sdk/dependencies.go"
)

for entry in "${ANCHORS[@]}"; do
	sym="${entry%%:*}"
	file="${entry#*:}"
	grep -q -- "$sym" "$DOC" || continue # not currently cited; nothing to anchor
	if [[ ! -f "$file" ]]; then
		echo "FAIL: $DOC cites '$sym' in $file, but $file no longer exists."
		fail=1
		continue
	fi
	# Match the declaration, not any mention: a rename that leaves a stale call
	# site or comment behind must still fail.
	if ! grep -qE "^func $sym\(" "$file"; then
		echo "FAIL: $DOC cites '$sym' in $file, but that symbol is gone."
		echo "      A fix likely renamed it. Update the prose that names it —"
		echo "      the document states a defect against that seam in past tense"
		echo "      and a reader needs the current symbol to check it."
		fail=1
	fi
done

# --- 2. Issue links and finding codes --------------------------------------
if ! command -v gh >/dev/null 2>&1 || ! gh auth status >/dev/null 2>&1; then
	echo "check_audit_doc: gh unavailable/unauthenticated — skipping link check."
	echo "check_audit_doc: code anchors checked."
	exit $fail
fi

# Rows look like: | F05 — description | [#416](https://github.com/owner/repo/issues/416) |
while IFS= read -r row; do
	code="$(sed -E 's/^\| ([FAE][0-9]+).*/\1/' <<<"$row")"
	url="$(grep -oE 'https://github\.com/codefly-dev/[a-z-]+/issues/[0-9]+' <<<"$row" | head -1)"
	[[ -n "$url" ]] || continue
	repo="$(sed -E 's|https://github.com/([^/]+/[^/]+)/issues/[0-9]+|\1|' <<<"$url")"
	num="$(sed -E 's|.*/issues/([0-9]+)|\1|' <<<"$url")"

	title="$(gh issue view "$num" --repo "$repo" --json title --jq .title 2>/dev/null || true)"
	if [[ -z "$title" ]]; then
		echo "FAIL: $DOC maps $code -> $repo#$num, which does not resolve."
		fail=1
		continue
	fi
	if [[ "$title" != *"$code"* ]]; then
		echo "FAIL: $DOC maps $code -> $repo#$num, but that issue is titled:"
		echo "      $title"
		echo "      The finding -> issue mapping drifted."
		fail=1
	fi
done < <(grep -E '^\| [FAE][0-9]+' "$DOC")

if [[ $fail -eq 0 ]]; then
	echo "check_audit_doc: code anchors and $(grep -cE '^\| [FAE][0-9]+' "$DOC") issue mappings ok."
fi
exit $fail
