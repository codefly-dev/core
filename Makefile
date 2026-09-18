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

# Fetches first: buf reads the baseline out of origin/main, so without it the
# gate silently answers against whatever that ref pointed at last.
.PHONY: buf-breaking
buf-breaking:
	git fetch origin main
	cd proto && $(BUF) breaking --against "../.git#ref=origin/main,subdir=proto"

# Companions: build images with scripts (run from core/)
#   ./companions/scripts/build_companions.sh
