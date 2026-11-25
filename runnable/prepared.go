package runnable

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	basev1 "github.com/codefly-dev/core/generated/go/codefly/base/v1"
	runnablev1 "github.com/codefly-dev/core/generated/go/codefly/runnable/v1"
)

// PreparedSchema is the schema of a prepared binding: one derived operation
// installed for one environment, carrying what a call needs and nothing a call
// does not. There is one schema and it names the current format; a value
// stating any other is refused by name. See docs/runnable-binding-delivery.md.
const PreparedSchema = "codefly.runnable-prepared/v1"

// earlierPreparedFields are the top-level fields of the forms a prepared
// binding had before this one: the canonical package and binding with a
// descriptor set shared by digest, and before that the descriptors embedded
// per operation. No field of the current form has any of these names, so a
// value carrying one is the earlier form whatever schema it states.
var earlierPreparedFields = []string{"package", "binding", "descriptor_set", "descriptorSet", "descriptors"}

// refuseEarlierPrepared refuses a value in an earlier form by what it carries,
// before its schema is read: an earlier value relabelled with the current
// schema is still not a call a caller can make.
func refuseEarlierPrepared(value []byte) error {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil {
		return nil // not a JSON object: the strict reader says so
	}
	for _, name := range earlierPreparedFields {
		if _, carried := fields[name]; carried {
			return fmt.Errorf("%w: prepared binding carries %q, a field of the earlier form that delivered the owner's package, binding and descriptors; that form is not read whatever schema it states: regenerate the runnable-bindings group with `codefly generate runnable-bindings`",
				ErrInvalid, name)
		}
	}
	return nil
}

// unknownPreparedSchema is the refusal a value stating another schema receives.
func unknownPreparedSchema(stated string) error {
	return fmt.Errorf("%w: prepared binding schema is %q, which is unknown; this core reads %s and no other: state the schema as %s",
		ErrInvalid, stated, PreparedSchema, PreparedSchema)
}

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
func ContractDigest(contract *basev1.RunnableContract) (string, error) {
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
func EncodePrepared(binding *runnablev1.PreparedBinding) ([]byte, error) {
	if binding == nil {
		return nil, fmt.Errorf("%w: prepared binding is required", ErrInvalid)
	}
	prepared := proto.CloneOf(binding)
	if prepared.GetSchema() != "" && prepared.GetSchema() != PreparedSchema {
		return nil, unknownPreparedSchema(prepared.GetSchema())
	}
	prepared.Schema = PreparedSchema
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
func DecodePrepared(value []byte) (*runnablev1.PreparedBinding, error) {
	if len(value) == 0 || len(value) > MaxPreparedBytes {
		return nil, fmt.Errorf("%w: prepared binding of %d bytes is outside (0, %d]", ErrInvalid, len(value), MaxPreparedBytes)
	}
	if err := refuseEarlierPrepared(value); err != nil {
		return nil, err
	}
	binding := &runnablev1.PreparedBinding{}
	if err := protojson.Unmarshal(value, binding); err != nil {
		return nil, fmt.Errorf("%w: prepared binding is not a %s document: %v", ErrInvalid, PreparedSchema, err)
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
func VerifyPrepared(binding *runnablev1.PreparedBinding) error {
	if binding == nil {
		return fmt.Errorf("%w: prepared binding is required", ErrInvalid)
	}
	if binding.GetSchema() != PreparedSchema {
		return unknownPreparedSchema(binding.GetSchema())
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
	policy := preparedPolicy(binding, codes)
	if err := policy.Validate(); err != nil {
		return err
	}
	// A GET is derived only as a read, and an installer holds a delivered one
	// to the same rule rather than trust that a generator applied it.
	if binding.GetCall().GetRest().GetVerb() == http.MethodGet {
		if err := readOnlyRoute(policy, binding.GetOperation().GetSpelling()); err != nil {
			return err
		}
	}
	return refuseUnresolvedSlots("prepared binding", policy)
}

// refuseUnresolvedSlots is the one refusal of a delivered policy with a hole
// in it. A slot is a question the owner asked the composition; a binding or a
// receipt is the composition's answer, and one delivered with the question
// still open was written before anyone answered. No installation may admit it.
func refuseUnresolvedSlots(what string, policy *OperationSpec) error {
	if len(policy.RequiredScopeSlots) == 0 {
		return nil
	}
	names := make([]string, 0, len(policy.RequiredScopeSlots))
	for _, slot := range policy.RequiredScopeSlots {
		names = append(names, slot.GetName())
	}
	return fmt.Errorf("%w: %w: %s for %q leaves scope slots %s unresolved; a composition resolves them with OperationSpec.ResolveScopeSlots before delivery",
		ErrInvalid, ErrUnresolvedScopeSlots, what, policy.Method, strings.Join(names, ", "))
}

// verifyPreparedRoute holds the dial target to the operation the binding names,
// and answers with the vocabulary that route's retryable codes are spelled in.
// The two are one check because they have one cause: a gRPC procedure and a
// REST route are different targets *and* different code vocabularies, and the
// form that read one spelling as both refused every REST operation — "POST
// /path" is not a method path.
func verifyPreparedRoute(binding *runnablev1.PreparedBinding) (CodeVocabulary, error) {
	spelling := binding.GetOperation().GetSpelling()
	switch route := binding.GetCall().GetRoute().(type) {
	case *runnablev1.PreparedCall_Connect:
		if procedure := route.Connect.GetProcedure(); procedure != spelling {
			return GRPCStatusNames, fmt.Errorf("%w: prepared binding calls procedure %q for operation %q", ErrInvalid, procedure, spelling)
		}
		return GRPCStatusNames, nil
	case *runnablev1.PreparedCall_Rest:
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
func preparedPolicy(binding *runnablev1.PreparedBinding, codes CodeVocabulary) *OperationSpec {
	return policySpec(binding.GetOperation().GetSpelling(), binding.GetPolicy(), codes)
}

// policySpec reads a delivered policy back as the spec core validates, for the
// operation spelling it was delivered for and the vocabulary its codes are
// spelled in. It is the one reader of a delivered Operation, as Policy is the
// one writer; TestPolicyRoundTripsEveryField holds the pair to the schema.
func policySpec(spelling string, declared *runnablev1.Operation, codes CodeVocabulary) *OperationSpec {
	return &OperationSpec{
		Method:             spelling,
		AttemptTimeout:     declared.GetAttemptTimeout().AsDuration(),
		TotalTimeout:       declared.GetTotalTimeout().AsDuration(),
		MaxAttempts:        declared.GetMaxAttempts(),
		Backoff:            declared.GetBackoff().AsDuration(),
		RetryableCodes:     declared.GetRetryableCodes(),
		Codes:              codes,
		Audience:           declared.GetAudience(),
		InvokeScopes:       declared.GetInvokeScopes(),
		LookupScopes:       declared.GetLookupScopes(),
		LookupMethod:       declared.GetLookupMethod(),
		MaxInputBytes:      declared.GetMaxInputBytes(),
		MaxOutputBytes:     declared.GetMaxOutputBytes(),
		Completion:         declared.GetCompletion(),
		Tool:               proto.CloneOf(declared.GetTool()),
		RequiredScopeSlots: clonedSlots(declared.GetRequiredScopeSlots()),
		Recovery:           declared.GetRecovery(),
	}
}
