GOBIN ?= $$(go env GOPATH)/bin

# Safety net: every `go test` inherits a hard timeout so no test run hangs.
# Target-specific -timeout flags still override this. Unknown flags are
# silently ignored by go build/vet, so this is safe to scope to all commands.
export GOFLAGS ?= -timeout=300s

# CGO-free surface guard: asserts every package builds with CGO_ENABLED=0
# except the documented cgo allowlist, so cgo creep fails here rather than at a
# downstream companion publish. See docs/cgo.md.
.PHONY: check-cgo-free
check-cgo-free:
	./scripts/check_cgo_free.sh

# Version guard: version/info.codefly.yaml drives composition's
# minimum-codefly-version gate, and nothing else in CI notices when it falls
# behind the published tags. See scripts/check_version_tag.sh.
.PHONY: check-version-tag
check-version-tag:
	./scripts/check_version_tag.sh

.PHONY: install-go-test-coverage
install-go-test-coverage:
	go install github.com/vladopajic/go-test-coverage/v2@latest

.PHONY: check-coverage
check-coverage: install-go-test-coverage
	go test ./... -coverprofile=./cover.out -covermode=atomic -coverpkg=./...
	${GOBIN}/go-test-coverage --config=./.testcoverage.yaml

# buf must be the one CI's schema gate and the proto companion's generator both
# run: a check that answers from a different buf is not a verdict on the gate
# that decides whether a schema lands. internal/ciguard's
# TestWorkflowBufMatchesTheCompanionImage fails when this pin, the workflow's
# buf-setup-action input and companions/proto/Dockerfile disagree.
#
# Invoked through `go run`, so the version is fixed by construction and a stray
# buf on PATH cannot be what answers.
BUF_VERSION := 1.71.0
BUF := go run github.com/bufbuild/buf/cmd/buf@v$(BUF_VERSION)

.PHONY: buf-lint
buf-lint:
	cd proto && $(BUF) lint

# `codefly generate proto --local` execs whatever `buf` is on PATH
# (codefly-dev/cli#744), and that buf is the one whose output gets committed.
# `go run` puts nothing on PATH, so the check targets cannot serve that path —
# this installs the same pin where generation will find it.
.PHONY: buf-install
buf-install:
	go install github.com/bufbuild/buf/cmd/buf@v$(BUF_VERSION)

# The remote holding the canonical repo, which is not always `origin`: on a fork
# checkout `origin` is the fork, whose main can be arbitrarily stale, and a
# baseline taken from it reports clean on a real break. Checked, not assumed.
BASE_REMOTE ?= origin

# The baseline is the MERGE BASE, not $(BASE_REMOTE)/main. CI checks out the
# pull request merged into main, so a package main gained after this branch was
# cut is present on both sides there. Compared against main's tip instead, that
# same package is missing from this branch only, and buf reports it as a
# deletion: `make buf-breaking` exits 100 naming a package the branch never
# touched. The merge base is what the branch actually changed, so it answers the
# question CI answers. Fetch first, or the merge base is computed against
# wherever the ref last pointed.
.PHONY: buf-breaking
buf-breaking:
	@git remote get-url $(BASE_REMOTE) | grep -qE 'codefly-dev/core(\.git)?$$' || \
		{ echo "BASE_REMOTE=$(BASE_REMOTE) is not codefly-dev/core: its main is not the baseline the gate uses. Re-run with BASE_REMOTE=<remote>." >&2; exit 1; }
	git fetch $(BASE_REMOTE) main
	cd proto && $(BUF) breaking --against "../.git#ref=$$(git merge-base $(BASE_REMOTE)/main HEAD),subdir=proto"

# Companions: build images with scripts (run from core/)
#   ./companions/scripts/build_companions.sh
