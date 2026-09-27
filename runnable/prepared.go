package runnable

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/descriptorpb"
)

// PreparedSchemaV2 is the schema of a prepared binding value that references
// its owner endpoint's descriptor set by digest. The embedded form that came
// before it carried no schema at all. See docs/runnable-binding-delivery.md.
const PreparedSchemaV2 = "codefly.runnable-prepared/v2"

// DescriptorSetKeyPrefix begins the configuration key a shared descriptor set
// is delivered under, beside the prepared values that reference it.
const DescriptorSetKeyPrefix = "DESCRIPTOR_SET__"

// MaxDescriptorSetBytes bounds one decoded descriptor set. A real owner
// endpoint's lean set is around 100 KB; nothing larger is parsed.
const MaxDescriptorSetBytes = 4 << 20

// MaxPreparedBytes bounds one prepared value: the canonical package and
// binding, the operation policy and one reference. It carries no descriptors.
const MaxPreparedBytes = 256 << 10

var (
	// ErrEmbeddedDescriptors refuses a prepared value in the embedded form,
	// which carried its own descriptor closure per operation.
	ErrEmbeddedDescriptors = errors.New("prepared binding embeds its descriptors: that form is no longer accepted, run `codefly generate runnable-bindings` to regenerate the runnable-bindings group")
	// ErrDescriptorSetMismatch refuses a delivered descriptor set whose bytes
	// are not the ones its reference names.
	ErrDescriptorSetMismatch = errors.New("descriptor set does not match its digest")
)

var descriptorSetDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

// Prepared is one value of the runnable-bindings workspace configuration: an
// operation installed for one environment.
type Prepared struct {
	// Schema is PreparedSchemaV2.
	Schema string `json:"schema"`
	// Package is the canonical RunnablePackage (CanonicalJSON).
	Package json.RawMessage `json:"package"`
	// Binding is the canonical RunnableBinding, prepared and verified.
	Binding json.RawMessage `json:"binding"`
	// Operation is the execution policy and authority the owner's method
	// declared. Its shape belongs to the writer and its reader; core carries
	// it without interpreting it.
	Operation json.RawMessage `json:"operation"`
	// DescriptorSet references the owner endpoint's descriptor set, delivered
	// once beside every value that references it.
	DescriptorSet *DescriptorSetReference `json:"descriptor_set,omitempty"`
}

// DescriptorSetReference names a shared descriptor set by content.
type DescriptorSetReference struct {
	// Digest is DescriptorSetDigest of the delivered (lean) set.
	Digest string `json:"digest"`
	// Contract is the API contract catalog's digest of the published
	// contract.binpb the set was derived from, recorded for provenance.
	Contract string `json:"contract"`
}

// Key is the configuration key the referenced set is delivered under.
func (r *DescriptorSetReference) Key() (string, error) {
	if r == nil {
		return "", fmt.Errorf("%w: descriptor set reference is required", ErrInvalid)
	}
	return DescriptorSetKey(r.Digest)
}

// DescriptorSetKey is the configuration key a descriptor set with this digest
// is delivered under: DESCRIPTOR_SET__ and the hex digest, upper-cased, so the
// SDK's workspace-value lookup finds it unchanged.
func DescriptorSetKey(digest string) (string, error) {
	if !descriptorSetDigestPattern.MatchString(digest) {
		return "", fmt.Errorf("%w: descriptor set digest %q is not sha256:<64 lowercase hex>", ErrInvalid, digest)
	}
	return DescriptorSetKeyPrefix + strings.ToUpper(strings.TrimPrefix(digest, "sha256:")), nil
}

// DescriptorSetDigest is the identity of a delivered descriptor set: the
// sha256 of its bytes.
func DescriptorSetDigest(set []byte) string {
	sum := sha256.Sum256(set)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// LeanDescriptorSet derives the set delivered for an owner endpoint from its
// published contract: every file kept, in order, with SourceCodeInfo removed,
// marshalled deterministically. Equal contracts give equal bytes, and so equal
// digests, on every machine running the same protobuf runtime.
func LeanDescriptorSet(contract []byte) ([]byte, error) {
	if len(contract) == 0 || len(contract) > MaxDescriptorSetBytes {
		return nil, fmt.Errorf("%w: contract of %d bytes is outside (0, %d]", ErrInvalid, len(contract), MaxDescriptorSetBytes)
	}
	set := &descriptorpb.FileDescriptorSet{}
	if err := proto.Unmarshal(contract, set); err != nil {
		return nil, fmt.Errorf("%w: contract is not a FileDescriptorSet: %v", ErrInvalid, err)
	}
	if len(set.GetFile()) == 0 {
		return nil, fmt.Errorf("%w: contract declares no file", ErrInvalid)
	}
	for _, file := range set.GetFile() {
		file.SourceCodeInfo = nil
	}
	return proto.MarshalOptions{Deterministic: true}.Marshal(set)
}

// EncodeDescriptorSet is the configuration value a descriptor set is delivered
// as: standard base64.
func EncodeDescriptorSet(set []byte) string {
	return base64.StdEncoding.EncodeToString(set)
}

// ResolveDescriptorSet reads the set a reference names through lookup (a
// workspace value lookup in the reference's group), and returns its bytes only
// when their digest is the referenced one.
func ResolveDescriptorSet(reference *DescriptorSetReference, lookup func(key string) (string, error)) ([]byte, error) {
	key, err := reference.Key()
	if err != nil {
		return nil, err
	}
	value, err := lookup(key)
	if err != nil {
		return nil, fmt.Errorf("descriptor set %s: %w", key, err)
	}
	if base64.StdEncoding.DecodedLen(len(value)) > MaxDescriptorSetBytes {
		return nil, fmt.Errorf("%w: descriptor set %s exceeds %d bytes", ErrInvalid, key, MaxDescriptorSetBytes)
	}
	set, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return nil, fmt.Errorf("%w: descriptor set %s is not standard base64", ErrInvalid, key)
	}
	if err := VerifyDescriptorSet(reference.Digest, set); err != nil {
		return nil, fmt.Errorf("descriptor set %s: %w", key, err)
	}
	return set, nil
}

// VerifyDescriptorSet refuses set unless it is a FileDescriptorSet whose digest
// is digest.
func VerifyDescriptorSet(digest string, set []byte) error {
	if !descriptorSetDigestPattern.MatchString(digest) {
		return fmt.Errorf("%w: descriptor set digest %q is not sha256:<64 lowercase hex>", ErrInvalid, digest)
	}
	if actual := DescriptorSetDigest(set); actual != digest {
		return fmt.Errorf("%w: delivered %s, referenced %s", ErrDescriptorSetMismatch, actual, digest)
	}
	if err := proto.Unmarshal(set, &descriptorpb.FileDescriptorSet{}); err != nil {
		return fmt.Errorf("%w: descriptor set is not a FileDescriptorSet: %v", ErrInvalid, err)
	}
	return nil
}

// DecodePrepared reads one runnable-bindings value strictly. The embedded form
// (no schema, an inline "descriptors" field) is refused with
// ErrEmbeddedDescriptors; unknown fields, trailing content and a malformed
// reference are refused as invalid.
func DecodePrepared(value []byte) (*Prepared, error) {
	if len(value) == 0 || len(value) > MaxPreparedBytes {
		return nil, fmt.Errorf("%w: prepared binding of %d bytes is outside (0, %d]", ErrInvalid, len(value), MaxPreparedBytes)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(value, &fields); err != nil {
		return nil, fmt.Errorf("%w: prepared binding is not a JSON object", ErrInvalid)
	}
	if _, embedded := fields["descriptors"]; embedded {
		return nil, ErrEmbeddedDescriptors
	}
	if _, stated := fields["schema"]; !stated {
		return nil, ErrEmbeddedDescriptors
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.DisallowUnknownFields()
	prepared := &Prepared{}
	if err := decoder.Decode(prepared); err != nil {
		return nil, fmt.Errorf("%w: prepared binding: %v", ErrInvalid, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing prepared binding content", ErrInvalid)
	}
	if prepared.Schema != PreparedSchemaV2 {
		return nil, fmt.Errorf("%w: prepared binding schema %q is not %q", ErrInvalid, prepared.Schema, PreparedSchemaV2)
	}
	if !present(prepared.Package) || !present(prepared.Binding) || !present(prepared.Operation) {
		return nil, fmt.Errorf("%w: prepared binding requires package, binding and operation", ErrInvalid)
	}
	if prepared.DescriptorSet != nil {
		if _, err := prepared.DescriptorSet.Key(); err != nil {
			return nil, err
		}
		if !descriptorSetDigestPattern.MatchString(prepared.DescriptorSet.Contract) {
			return nil, fmt.Errorf("%w: descriptor set contract digest %q is not sha256:<64 lowercase hex>", ErrInvalid, prepared.DescriptorSet.Contract)
		}
	}
	return prepared, nil
}

func present(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null"))
}
