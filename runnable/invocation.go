package runnable

import (
	"bytes"
	"encoding/json"
	"fmt"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// PrepareInvocation clones inv and validates it against the verified package
// it invokes. Unlike a package or a binding an invocation is not
// content-addressed: it names one call against an installed binding rather
// than an immutable installation fact, so there is no digest to populate.
func PrepareInvocation(inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage) (*basev0.RunnableInvocation, error) {
	if inv == nil {
		return nil, fmt.Errorf("%w: invocation is required", ErrInvalid)
	}
	if err := VerifyPackage(pkg); err != nil {
		return nil, err
	}
	prepared := &basev0.RunnableInvocation{}
	proto.Merge(prepared, inv)
	if err := validateInvocation(prepared, pkg); err != nil {
		return nil, err
	}
	return prepared, nil
}

// ParseResult decodes and validates what an implementation answered for inv.
// Its error is what makes an outcome SERVED_INVOCATION_INTEGRITY rather than a
// completion, so every caller of a served operation draws that line in the same
// place.
func ParseResult(document []byte, inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage) (*basev0.RunnableResult, error) {
	result := &basev0.RunnableResult{}
	// A field this core does not know belongs to an implementation generated
	// from newer sources. Rejecting it would turn every invocation of that
	// runnable into an uncertain outcome, recomputing pure work and
	// re-reconciling effectful work that in fact completed. Unknown fields are
	// dropped here, unlike on the installation facts, whose digest would
	// silently stop covering them.
	if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(document, result); err != nil {
		return nil, fmt.Errorf("%w: result is not a %s document: %v", ErrInvalid, inv.GetProtocol(), err)
	}
	if err := validateResult(result, inv, pkg); err != nil {
		return nil, err
	}
	return result, nil
}

func validateInvocation(inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage) error {
	if err := validator.Validate(inv); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if inv.GetProtocol() != pkg.GetContract().GetProtocol() {
		return fmt.Errorf("%w: invocation protocol %q is not the package's %q", ErrInvalid, inv.GetProtocol(), pkg.GetContract().GetProtocol())
	}
	if !proto.Equal(inv.GetRunnable(), pkg.GetIdentity()) {
		return fmt.Errorf("%w: invocation names another release than the package it runs", ErrInvalid)
	}
	budget := inv.GetDeadline().AsTime().Sub(inv.GetIssuedAt().AsTime())
	if budget <= 0 {
		return fmt.Errorf("%w: invocation deadline is not after the instant it was issued", ErrInvalid)
	}
	// The declared timeout bounds one invocation's duration. A caller computing
	// a deadline of its own may shorten that, never overrule it.
	if declared := pkg.GetExecution().GetTimeout().AsDuration(); budget > declared {
		return fmt.Errorf("%w: invocation allows %s but the package declares a timeout of %s", ErrInvalid, budget, declared)
	}
	if pkg.GetExecution().GetRecovery() == basev0.RunnableExecution_RECOVERY_RECEIPT && inv.GetEffectId() == "" {
		return fmt.Errorf("%w: package declares %s, so the invocation must carry the effect identity an uncertain outcome is resolved with",
			ErrInvalid, basev0.RunnableExecution_RECOVERY_RECEIPT)
	}
	return validatePayload("input", inv.GetInput(), pkg.GetExecution().GetMaxInputBytes())
}

func validateResult(result *basev0.RunnableResult, inv *basev0.RunnableInvocation, pkg *basev0.RunnablePackage) error {
	if err := validator.Validate(result); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	if result.GetProtocol() != inv.GetProtocol() {
		return fmt.Errorf("%w: result protocol %q is not the invocation's %q", ErrInvalid, result.GetProtocol(), inv.GetProtocol())
	}
	if result.GetInvocationId() != inv.GetInvocationId() {
		return fmt.Errorf("%w: result belongs to invocation %q, not %q", ErrInvalid, result.GetInvocationId(), inv.GetInvocationId())
	}
	switch result.GetStatus() {
	case basev0.RunnableResult_SUCCEEDED:
		if result.GetError() != nil {
			return fmt.Errorf("%w: a succeeded result carries no error", ErrInvalid)
		}
		return validatePayload("output", result.GetOutput(), pkg.GetExecution().GetMaxOutputBytes())
	case basev0.RunnableResult_FAILED:
		if len(result.GetOutput()) > 0 {
			return fmt.Errorf("%w: a failed result carries no output", ErrInvalid)
		}
		if result.GetError().GetCode() == "" {
			return fmt.Errorf("%w: a failed result requires the handler's failure code", ErrInvalid)
		}
		return nil
	case basev0.RunnableResult_INTERRUPTED:
		if len(result.GetOutput()) > 0 {
			return fmt.Errorf("%w: an interrupted result carries no output", ErrInvalid)
		}
		return nil
	default:
		return fmt.Errorf("%w: result status %s is not a completion an implementation can report", ErrInvalid, result.GetStatus())
	}
}

// validatePayload frames a payload without type-checking it: the caller bounds
// the document and proves it is the object shape the profile promises, while
// the implementation checks it against the typed bindings generated from the
// contract.
func validatePayload(kind string, payload []byte, bound uint64) error {
	if uint64(len(payload)) > bound {
		return fmt.Errorf("%w: %s is %d bytes, over the declared bound of %d", ErrInvalid, kind, len(payload), bound)
	}
	trimmed := bytes.TrimLeft(payload, " \t\r\n")
	if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(payload) {
		return fmt.Errorf("%w: %s must be one JSON object", ErrInvalid, kind)
	}
	return nil
}

// DeadlineFormat is how the deadline header spells an instant: RFC 3339 with
// nanoseconds, in UTC. It is pinned beside the header rather than left to each
// caller because a receiver that parses one spelling and a caller that writes
// another do not fail visibly — the receiver reads no deadline and runs work
// nobody is still waiting for.
const DeadlineFormat = time.RFC3339Nano
