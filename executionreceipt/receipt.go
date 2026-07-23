// Package executionreceipt validates, digests, signs, and verifies Codefly's
// product-neutral execution receipt contract.
package executionreceipt

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"buf.build/go/protovalidate"
	executionv1 "github.com/codefly-dev/core/generated/go/codefly/execution/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// SchemaV1 is the only receipt schema accepted by this package.
	SchemaV1 = "codefly.execution-receipt/v1"
	// SignatureAlgorithm is the attestation signature algorithm.
	SignatureAlgorithm = "Ed25519"
)

var (
	// ErrInvalid is returned for a malformed or internally inconsistent receipt.
	ErrInvalid = errors.New("invalid Codefly execution receipt")

	receiptValidator  protovalidate.Validator
	namespacedPattern = regexp.MustCompile(
		`^[a-z][a-z0-9]*(?:-[a-z0-9]+)*(?:\.[a-z][a-z0-9]*(?:-[a-z0-9]+)*)+$`,
	)
)

func init() {
	var err error
	receiptValidator, err = protovalidate.New()
	if err != nil {
		panic(err)
	}
}

// Prepare clones receipt, validates its immutable facts, and populates the
// deterministic payload digest. A non-empty caller-supplied digest must already
// match; Prepare never silently repairs conflicting immutable bytes.
func Prepare(receipt *executionv1.ExecutionReceiptV1) (*executionv1.ExecutionReceiptV1, error) {
	if receipt == nil {
		return nil, fmt.Errorf("%w: receipt is required", ErrInvalid)
	}
	prepared := proto.Clone(receipt).(*executionv1.ExecutionReceiptV1)
	suppliedDigest := prepared.GetPayloadSha256()
	prepared.PayloadSha256 = ""
	if err := validateReceipt(prepared, false); err != nil {
		return nil, err
	}
	payload, err := deterministicReceiptBytes(prepared)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(payload)
	computedDigest := hex.EncodeToString(sum[:])
	if suppliedDigest != "" && suppliedDigest != computedDigest {
		return nil, fmt.Errorf("%w: payload_sha256 does not match immutable receipt bytes", ErrInvalid)
	}
	prepared.PayloadSha256 = computedDigest
	if err := validateReceipt(prepared, true); err != nil {
		return nil, err
	}
	return prepared, nil
}

// Attest prepares receipt and signs its deterministic protobuf bytes with one
// registered Gateway key. The input message is never mutated.
func Attest(
	receipt *executionv1.ExecutionReceiptV1,
	signerID string,
	keyID string,
	privateKey ed25519.PrivateKey,
) (*executionv1.ExecutionAttestationV1, error) {
	if len(privateKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("%w: Ed25519 private key must be %d bytes", ErrInvalid, ed25519.PrivateKeySize)
	}
	prepared, err := Prepare(receipt)
	if err != nil {
		return nil, err
	}
	payload, err := deterministicReceiptBytes(prepared)
	if err != nil {
		return nil, err
	}
	attestation := &executionv1.ExecutionAttestationV1{
		Receipt:   prepared,
		SignerId:  signerID,
		KeyId:     keyID,
		Algorithm: SignatureAlgorithm,
		Signature: ed25519.Sign(privateKey, payload),
	}
	if err := receiptValidator.Validate(attestation); err != nil {
		return nil, fmt.Errorf("%w: attestation shape: %v", ErrInvalid, err)
	}
	return attestation, nil
}

// Verify validates every receipt invariant, recomputes its payload digest, and
// verifies the Gateway signature. The returned receipt is a defensive clone.
func Verify(
	attestation *executionv1.ExecutionAttestationV1,
	publicKey ed25519.PublicKey,
) (*executionv1.ExecutionReceiptV1, error) {
	if attestation == nil {
		return nil, fmt.Errorf("%w: attestation is required", ErrInvalid)
	}
	if len(publicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("%w: Ed25519 public key must be %d bytes", ErrInvalid, ed25519.PublicKeySize)
	}
	if err := receiptValidator.Validate(attestation); err != nil {
		return nil, fmt.Errorf("%w: attestation shape: %v", ErrInvalid, err)
	}
	prepared, err := Prepare(attestation.GetReceipt())
	if err != nil {
		return nil, err
	}
	payload, err := deterministicReceiptBytes(prepared)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(publicKey, payload, attestation.GetSignature()) {
		return nil, fmt.Errorf("%w: attestation signature verification failed", ErrInvalid)
	}
	return prepared, nil
}

func validateReceipt(receipt *executionv1.ExecutionReceiptV1, requireDigest bool) error {
	shape := proto.Clone(receipt).(*executionv1.ExecutionReceiptV1)
	if !requireDigest {
		shape.PayloadSha256 = string(bytes.Repeat([]byte{'0'}, sha256.Size*2))
	}
	if err := receiptValidator.Validate(shape); err != nil {
		return fmt.Errorf("%w: receipt shape: %v", ErrInvalid, err)
	}
	if receipt.GetStage() == executionv1.ExecutionStage_EXECUTION_STAGE_UNSPECIFIED {
		return fmt.Errorf("%w: stage is required", ErrInvalid)
	}
	if receipt.GetAssurance() == executionv1.ExecutionAssurance_EXECUTION_ASSURANCE_UNSPECIFIED {
		return fmt.Errorf("%w: assurance is required", ErrInvalid)
	}
	if !validNamespaced(receipt.GetOperationKind()) {
		return fmt.Errorf("%w: operation_kind must be a canonical namespaced value", ErrInvalid)
	}
	if !validNamespaced(receipt.GetProducer().GetId()) {
		return fmt.Errorf("%w: producer.id must be a canonical namespaced value", ErrInvalid)
	}
	if err := validateTimestamp("started_at", receipt.GetStartedAt()); err != nil {
		return err
	}
	terminal := isTerminal(receipt.GetStage())
	if terminal && receipt.GetCompletedAt() == nil {
		return fmt.Errorf("%w: terminal receipt requires completed_at", ErrInvalid)
	}
	if !terminal && receipt.GetCompletedAt() != nil {
		return fmt.Errorf("%w: non-terminal receipt cannot set completed_at", ErrInvalid)
	}
	if receipt.GetCompletedAt() != nil {
		if err := validateTimestamp("completed_at", receipt.GetCompletedAt()); err != nil {
			return err
		}
		if receipt.GetCompletedAt().AsTime().Before(receipt.GetStartedAt().AsTime()) {
			return fmt.Errorf("%w: completed_at precedes started_at", ErrInvalid)
		}
	}
	if (receipt.ExtensionSchema == nil) != (receipt.ExtensionJson == nil) {
		return fmt.Errorf("%w: extension_schema and extension_json must be present together", ErrInvalid)
	}
	if receipt.ExtensionJson != nil && !json.Valid(receipt.GetExtensionJson()) {
		return fmt.Errorf("%w: extension_json is not valid JSON", ErrInvalid)
	}
	seenResources := make(map[string]struct{}, len(receipt.GetResources()))
	for _, resource := range receipt.GetResources() {
		if resource == nil {
			return fmt.Errorf("%w: resources cannot contain nil", ErrInvalid)
		}
		if !validNamespaced(resource.GetKind()) {
			return fmt.Errorf("%w: resource.kind must be a canonical namespaced value", ErrInvalid)
		}
		key := resource.GetKind() + "\x00" + resource.GetReference()
		if _, exists := seenResources[key]; exists {
			return fmt.Errorf("%w: duplicate resource kind/reference", ErrInvalid)
		}
		seenResources[key] = struct{}{}
	}
	return nil
}

func deterministicReceiptBytes(receipt *executionv1.ExecutionReceiptV1) ([]byte, error) {
	payload, err := (proto.MarshalOptions{Deterministic: true}).Marshal(receipt)
	if err != nil {
		return nil, fmt.Errorf("%w: marshal deterministic receipt: %v", ErrInvalid, err)
	}
	return payload, nil
}

func validateTimestamp(name string, value *timestamppb.Timestamp) error {
	if value == nil {
		return fmt.Errorf("%w: %s is required", ErrInvalid, name)
	}
	if err := value.CheckValid(); err != nil {
		return fmt.Errorf("%w: %s: %v", ErrInvalid, name, err)
	}
	if value.AsTime().Location() != time.UTC {
		return fmt.Errorf("%w: %s must be UTC", ErrInvalid, name)
	}
	return nil
}

func isTerminal(stage executionv1.ExecutionStage) bool {
	switch stage {
	case executionv1.ExecutionStage_EXECUTION_STAGE_SUCCEEDED,
		executionv1.ExecutionStage_EXECUTION_STAGE_FAILED,
		executionv1.ExecutionStage_EXECUTION_STAGE_COMPENSATED,
		executionv1.ExecutionStage_EXECUTION_STAGE_UNCERTAIN:
		return true
	default:
		return false
	}
}

func validNamespaced(value string) bool {
	return namespacedPattern.MatchString(value)
}
