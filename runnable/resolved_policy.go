package runnable

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
)

// ResolvedPolicySchemaV1 is the schema of a resolved policy receipt: one
// operation's concrete policy, every required scope slot answered, as both
// halves of an installation receive it. See docs/runnable-binding-delivery.md.
const ResolvedPolicySchemaV1 = "codefly.runnable-resolved-policy/v1"

// PolicyDigestFormatV1 is mixed into a policy digest so a change in how the
// digest is computed changes every digest instead of colliding with the
// previous format. It is distinct from ContractDigestFormatV1 on purpose: the
// contract digest covers input and output and nothing else, and stays so.
const PolicyDigestFormatV1 = "codefly.runnable-policy.digest/v1"

// PolicyDigest is the identity of a concrete policy: the sha256 of its
// canonical form, prefixed by the digest format. Two installations that hold
// the same digest hold the same audience, scopes, budget and exposure, down to
// the order of the selected ids.
func PolicyDigest(policy *runnablev0.Operation) (string, error) {
	canonical, err := CanonicalJSON(policy)
	if err != nil {
		return "", fmt.Errorf("%w: policy: %v", ErrInvalid, err)
	}
	hash := sha256.New()
	hash.Write([]byte(PolicyDigestFormatV1))
	hash.Write([]byte{0})
	hash.Write(canonical)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

// EncodeResolvedPolicy returns the receipt as it is handed to a host: canonical
// proto3 JSON. The schema and the policy digest are filled when the writer
// leaves them empty and a stated value that is not the right one is refused
// rather than repaired, exactly as EncodePrepared treats the contract digest.
func EncodeResolvedPolicy(resolved *runnablev0.ResolvedPolicy) ([]byte, error) {
	if resolved == nil {
		return nil, fmt.Errorf("%w: resolved policy is required", ErrInvalid)
	}
	receipt := proto.CloneOf(resolved)
	if receipt.GetSchema() != "" && receipt.GetSchema() != ResolvedPolicySchemaV1 {
		return nil, fmt.Errorf("%w: resolved policy states schema %q, and this core writes %s", ErrInvalid, receipt.GetSchema(), ResolvedPolicySchemaV1)
	}
	receipt.Schema = ResolvedPolicySchemaV1
	digest, err := PolicyDigest(receipt.GetPolicy())
	if err != nil {
		return nil, err
	}
	if receipt.GetPolicyDigest() != "" && receipt.GetPolicyDigest() != digest {
		return nil, fmt.Errorf("%w: resolved policy states policy digest %s, and its policy derives %s", ErrInvalid, receipt.GetPolicyDigest(), digest)
	}
	receipt.PolicyDigest = digest
	if err = VerifyResolvedPolicy(receipt); err != nil {
		return nil, err
	}
	return CanonicalJSON(receipt)
}

// DecodeResolvedPolicy reads a receipt strictly: a field the schema does not
// declare is refused rather than dropped, as DecodePrepared refuses it.
func DecodeResolvedPolicy(value []byte) (*runnablev0.ResolvedPolicy, error) {
	if len(value) == 0 || len(value) > MaxPreparedBytes {
		return nil, fmt.Errorf("%w: resolved policy of %d bytes is outside (0, %d]", ErrInvalid, len(value), MaxPreparedBytes)
	}
	resolved := &runnablev0.ResolvedPolicy{}
	if err := protojson.Unmarshal(value, resolved); err != nil {
		return nil, fmt.Errorf("%w: resolved policy is not a %s document: %v", ErrInvalid, ResolvedPolicySchemaV1, err)
	}
	if err := VerifyResolvedPolicy(resolved); err != nil {
		return nil, err
	}
	return resolved, nil
}

// VerifyResolvedPolicy refuses a receipt a host must not bind from: the wire
// contract's own bounds, a digest that does not cover the policy delivered
// with it, a policy outside the bounds an installation enforces, and a policy
// with a required scope slot still open — a receipt is the answer to the
// slots, and one that still asks the question is no receipt.
func VerifyResolvedPolicy(resolved *runnablev0.ResolvedPolicy) error {
	if resolved == nil {
		return fmt.Errorf("%w: resolved policy is required", ErrInvalid)
	}
	if err := validator.Validate(resolved); err != nil {
		return fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	digest, err := PolicyDigest(resolved.GetPolicy())
	if err != nil {
		return err
	}
	if resolved.GetPolicyDigest() != digest {
		return fmt.Errorf("%w: resolved policy carries policy digest %s, and its policy derives %s", ErrInvalid, resolved.GetPolicyDigest(), digest)
	}
	spelling := resolved.GetOperation().GetSpelling()
	spec := policySpec(spelling, resolved.GetPolicy(), vocabularyOf(spelling))
	if err := spec.Validate(); err != nil {
		return err
	}
	return refuseUnresolvedSlots("resolved policy", spec)
}

// BindingMatchesResolvedPolicy is the install-side equality gate: the prepared
// binding delivered to a caller carries exactly the policy the receipt handed
// to the host, for the same operation. It is what proves the two halves of an
// installation were generated from one resolution — not that each is valid on
// its own, which VerifyPrepared and VerifyResolvedPolicy already say, and not
// that the runtime will compare them again, which is the runtime's own check.
func BindingMatchesResolvedPolicy(binding *runnablev0.PreparedBinding, resolved *runnablev0.ResolvedPolicy) error {
	if err := VerifyPrepared(binding); err != nil {
		return err
	}
	if err := VerifyResolvedPolicy(resolved); err != nil {
		return err
	}
	if !proto.Equal(binding.GetOperation(), resolved.GetOperation()) {
		return fmt.Errorf("%w: prepared binding is for operation %q of %s/%s and the resolved policy for %q of %s/%s",
			ErrInvalid, binding.GetOperation().GetSpelling(), binding.GetOperation().GetModule(), binding.GetOperation().GetService(),
			resolved.GetOperation().GetSpelling(), resolved.GetOperation().GetModule(), resolved.GetOperation().GetService())
	}
	delivered, err := PolicyDigest(binding.GetPolicy())
	if err != nil {
		return err
	}
	if delivered != resolved.GetPolicyDigest() || !proto.Equal(binding.GetPolicy(), resolved.GetPolicy()) {
		return fmt.Errorf("%w: prepared binding for %q carries policy %s and the resolved policy handed to the host is %s; the two halves of this installation were not generated from one resolution",
			ErrInvalid, binding.GetOperation().GetSpelling(), delivered, resolved.GetPolicyDigest())
	}
	return nil
}

// vocabularyOf reads the retryable-code vocabulary off an operation spelling:
// a gRPC method is spelled "/package.Service/Method" and names codes by their
// google.rpc.Code name; a REST operation is spelled "VERB /path" and names them
// by HTTP status. The prepared binding reads the same fact off its route; a
// receipt has no route, and the spelling is the one thing both carry.
func vocabularyOf(spelling string) CodeVocabulary {
	if strings.HasPrefix(spelling, "/") {
		return GRPCStatusNames
	}
	return HTTPStatusCodes
}
