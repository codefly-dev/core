#!/usr/bin/env bash
# Enforces core's CGO-free surface contract: every package in the module MUST
# build with CGO_ENABLED=0, except a small, explicit allowlist of packages that
# are permitted to require cgo. cgo-ness is a property of the import graph
# resolved at link time — a single new import can silently drag a cgo-only
# package (today: the tree-sitter grammars under code/semantic) into the graph,
# and nothing fails until a downstream CGO_ENABLED=0 build (companion publish, an
# alpine image) tries to link, weeks later and one repo away.
#
# This check fails IN CORE, at the PR that introduces the creep, with a clear
# message. Builds are done with -tags codefly_nosemantic so the supported split
# constructor (code/codeserver.New) selects its CGO-free variant.
#
# See docs/cgo.md for the contract and the ownership boundary.

set -euo pipefail

# Packages permitted to require cgo. Adding an entry is a deliberate, reviewed
# decision — a new cgo dependency does not belong in the CGO-free surface unless
# there is no alternative. Keep this list and docs/cgo.md in sync.
CGO_ALLOWLIST=(
	"github.com/codefly-dev/core/code/semantic"
)

cd "$(git rev-parse --show-toplevel)"

grep_args=()
for pkg in "${CGO_ALLOWLIST[@]}"; do
	grep_args+=(-e "$pkg")
done

# .GoFiles excludes test-only packages, which `go build` rejects with "no
# non-test Go files" — those carry no linkable surface anyway.
pkgs=()
while IFS= read -r line; do
	pkgs+=("$line")
done < <(go list -f '{{if .GoFiles}}{{.ImportPath}}{{end}}' ./... | grep -vxF "${grep_args[@]}")

echo "Checking CGO-free surface: CGO_ENABLED=0 go build -tags codefly_nosemantic"
echo "Allowlist (permitted to require cgo): ${CGO_ALLOWLIST[*]}"

if CGO_ENABLED=0 go build -tags codefly_nosemantic "${pkgs[@]}"; then
	echo "OK: the CGO-free surface links without cgo."
	exit 0
fi

cat >&2 <<'EOF'

============================================================================
CGO creep detected: a package outside the cgo allowlist no longer builds with
CGO_ENABLED=0.

A "build constraints exclude all Go files" error above means a cgo-only package
(e.g. the tree-sitter grammars under code/code/semantic) has entered the import
graph of a package that is supposed to stay CGO-free. Downstream CGO-free builds
(companion publish, alpine images) will fail to link.

Fix one of:
  * Remove the new cgo dependency from the CGO-free surface. If it is optional,
    gate it behind the codefly_nosemantic build tag (see code/codeserver for the
    pattern) so the default build keeps it and the tagged build drops it.
  * If the package legitimately must require cgo, add it to CGO_ALLOWLIST in
    this script and document why in docs/cgo.md — a conscious, reviewed choice.
============================================================================
EOF
exit 1
