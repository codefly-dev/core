GOBIN ?= $$(go env GOPATH)/bin

# Safety net: every `go test` inherits a hard timeout so no test run hangs.
# Target-specific -timeout flags still override this. Unknown flags are
# silently ignored by go build/vet, so this is safe to scope to all commands.
export GOFLAGS ?= -timeout=300s

.PHONY: install-go-test-coverage
install-go-test-coverage:
	go install github.com/vladopajic/go-test-coverage/v2@latest

.PHONY: check-coverage
check-coverage: install-go-test-coverage
	go test ./... -coverprofile=./cover.out -covermode=atomic -coverpkg=./...
	${GOBIN}/go-test-coverage --config=./.testcoverage.yaml

# Companions: build images with scripts (run from core/)
#   ./companions/scripts/build_companions.sh
# LSP tests are normal Go tests: go test ./companions/golang/ -run TestLSP