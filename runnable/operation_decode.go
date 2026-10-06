package runnable

import (
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"

	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
)

// EncodeOperation writes a policy as the canonical proto3 JSON DecodeOperation
// reads, for every copy of a policy that is not a prepared binding: a derived
// catalog's operation document, a composition's record of what it selected.
func EncodeOperation(declared *runnablev0.Operation) ([]byte, error) {
	if declared == nil {
		return nil, fmt.Errorf("%w: operation policy is required", ErrInvalid)
	}
	return CanonicalJSON(declared)
}

// DecodeOperation reads a policy strictly: a field this core does not declare
// is refused rather than dropped. A reader that drops a field it does not know
// turns a requirement it never saw into an authority that was never required —
// an older reader handed a policy carrying required_scope_slots would write a
// prepared binding with the hole already erased, and VerifyPrepared, seeing
// only the fixed scopes, could not tell. Refusing here is the only place that
// can: a derived operation document decoded with a lenient decoder has already
// lost what it did not know by the time anything validates it.
func DecodeOperation(value []byte) (*runnablev0.Operation, error) {
	if len(value) == 0 || len(value) > MaxPreparedBytes {
		return nil, fmt.Errorf("%w: operation policy of %d bytes is outside (0, %d]", ErrInvalid, len(value), MaxPreparedBytes)
	}
	declared := &runnablev0.Operation{}
	if err := protojson.Unmarshal(value, declared); err != nil {
		return nil, fmt.Errorf("%w: operation policy is not a codefly.runnable.v0.Operation document: %v", ErrInvalid, err)
	}
	return declared, nil
}
