package runnable

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
)

// PreparedSchemaV3 is the schema of a prepared binding: one derived operation
// installed for one environment, carrying what a call needs and nothing a call
// does not. See docs/runnable-binding-delivery.md.
const PreparedSchemaV3 = "codefly.runnable-prepared/v3"

// ContractDigestFormatV1 is mixed into a contract digest so a change in how the
// digest is computed changes every digest instead of colliding with the
// previous format.
const ContractDigestFormatV1 = "codefly.runnable-contract.digest/v1"

// MaxPreparedBytes bounds one prepared value while it is still only bytes. A
// real value is a few hundred bytes to a few kilobytes — the operation, the
// call, the bounded contract and the declared policy — so this bounds parsing
// rather than describing what is delivered: the widest contract the projection
// profile admits is far larger than anything an owner publishes.
const MaxPreparedBytes = 256 << 10

// ContractDigest is the identity of a bounded contract: the sha256 of its
// canonical form, prefixed by the digest format. An owner whose published
// contract no longer derives the digest a prepared binding carries has drifted
// from what its callers were prepared for.
func ContractDigest(contract *basev0.RunnableContract) (string, error) {
	canonical, err := CanonicalJSON(contract)
	if err != nil {
		return "", fmt.Errorf("%w: contract: %v", ErrInvalid, err)
	}
	hash := sha256.New()
	hash.Write([]byte(ContractDigestFormatV1))
	hash.Write([]byte{0})
	hash.Write(canonical)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// EncodePrepared returns the configuration value binding is delivered as:
// canonical proto3 JSON. The schema and the contract digest are filled when the
// caller leaves them empty, so a writer can neither forget the digest nor
// compute it another way; a stated value that is not the right one is refused
// rather than repaired, exactly as a supplied package digest is. A value whose
// digest does not cover its own contract would make every later drift check
// meaningless, and one claiming another schema was written for another reader.
func EncodePrepared(binding *runnablev0.PreparedBinding) ([]byte, error) {
	if binding == nil {
		return nil, fmt.Errorf("%w: prepared binding is required", ErrInvalid)
	}
	prepared := proto.CloneOf(binding)
	if prepared.GetSchema() != "" && prepared.GetSchema() != PreparedSchemaV3 {
		return nil, fmt.Errorf("%w: prepared binding states schema %q, and this core writes %s",
			ErrInvalid, prepared.GetSchema(), PreparedSchemaV3)
	}
	prepared.Schema = PreparedSchemaV3
	digest, err := ContractDigest(prepared.GetContract())
	if err != nil {
		return nil, err
	}
	if prepared.GetContractDigest() != "" && prepared.GetContractDigest() != digest {
		return nil, fmt.Errorf("%w: prepared binding states contract digest %s, and its contract derives %s",
			ErrInvalid, prepared.GetContractDigest(), digest)
	}
	prepared.ContractDigest = digest
	if err = VerifyPrepared(prepared); err != nil {
		return nil, err
	}
	return CanonicalJSON(prepared)
}

// DecodePrepared reads one runnable-bindings value strictly. A field the schema
// does not declare is refused rather than dropped: an unknown field in an
// installation fact is either another schema's value or a field this reader
// would have had to act on, and neither is something to install past.
func DecodePrepared(value []byte) (*runnablev0.PreparedBinding, error) {
	if len(value) == 0 || len(value) > MaxPreparedBytes {
		return nil, fmt.Errorf("%w: prepared binding of %d bytes is outside (0, %d]", ErrInvalid, len(value), MaxPreparedBytes)
	}
	binding := &runnablev0.PreparedBinding{}
	if err := protojson.Unmarshal(value, binding); err != nil {
		return nil, fmt.Errorf("%w: prepared binding is not a %s document: %v", ErrInvalid, PreparedSchemaV3, err)
	}
	if err := VerifyPrepared(binding); err != nil {
		return nil, err
	}
	return binding, nil
}

// VerifyPrepared refuses a prepared binding a caller must not act on: the wire
// contract's own bounds, a contract digest that does not cover the contract
// delivered with it, a route that is not the operation the binding names, and a
// policy outside the bounds an installation enforces.
func VerifyPrepared(binding *runnablev0.PreparedBinding) error {
	if binding == nil {
		return fmt.Errorf("%w: prepared binding is required", ErrInvalid)
	}
	if err := validator.Validate(binding); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	digest, err := ContractDigest(binding.GetContract())
	if err != nil {
		return err
	}
	if binding.GetContractDigest() != digest {
		return fmt.Errorf("%w: prepared binding carries contract digest %s, and its contract derives %s",
			ErrInvalid, binding.GetContractDigest(), digest)
	}
	codes, err := verifyPreparedRoute(binding)
	if err != nil {
		return err
	}
	return preparedPolicy(binding, codes).Validate()
}

// verifyPreparedRoute holds the dial target to the operation the binding names,
// and answers with the vocabulary that route's retryable codes are spelled in.
// The two are one check because they have one cause: a gRPC procedure and a
// REST route are different targets *and* different code vocabularies, and the
// form that read one spelling as both refused every REST operation — "POST
// /path" is not a method path.
func verifyPreparedRoute(binding *runnablev0.PreparedBinding) (CodeVocabulary, error) {
	spelling := binding.GetOperation().GetSpelling()
	switch route := binding.GetCall().GetRoute().(type) {
	case *runnablev0.PreparedCall_Connect:
		if procedure := route.Connect.GetProcedure(); procedure != spelling {
			return GRPCStatusNames, fmt.Errorf("%w: prepared binding calls procedure %q for operation %q", ErrInvalid, procedure, spelling)
		}
		return GRPCStatusNames, nil
	case *runnablev0.PreparedCall_Rest:
		if spelled := Route(route.Rest.GetVerb(), route.Rest.GetPath()); spelled != spelling {
			return HTTPStatusCodes, fmt.Errorf("%w: prepared binding calls route %q for operation %q", ErrInvalid, spelled, spelling)
		}
		return HTTPStatusCodes, nil
	default:
		return GRPCStatusNames, fmt.Errorf("%w: prepared binding for operation %q names no route to call", ErrInvalid, spelling)
	}
}

// preparedPolicy reads the declared policy back as the spec core validates, so
// a policy that installs is a policy that would have generated: the bounds live
// in OperationSpec.Validate and are applied to what was delivered rather than
// trusted because a generator once checked them.
func preparedPolicy(binding *runnablev0.PreparedBinding, codes CodeVocabulary) *OperationSpec {
	declared := binding.GetPolicy()
	return &OperationSpec{
		Method:         binding.GetOperation().GetSpelling(),
		AttemptTimeout: declared.GetAttemptTimeout().AsDuration(),
		TotalTimeout:   declared.GetTotalTimeout().AsDuration(),
		MaxAttempts:    declared.GetMaxAttempts(),
		Backoff:        declared.GetBackoff().AsDuration(),
		RetryableCodes: declared.GetRetryableCodes(),
		Codes:          codes,
		Audience:       declared.GetAudience(),
		InvokeScopes:   declared.GetInvokeScopes(),
		LookupScopes:   declared.GetLookupScopes(),
		LookupMethod:   declared.GetLookupMethod(),
		MaxInputBytes:  declared.GetMaxInputBytes(),
		MaxOutputBytes: declared.GetMaxOutputBytes(),
		Completion:     declared.GetCompletion(),
	}
}
