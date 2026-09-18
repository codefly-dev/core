package runnable

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

const (
	// PackageDigestFormatV1 is mixed into the package digest so a change in how
	// the digest is computed changes every digest instead of colliding with
	// the previous format.
	PackageDigestFormatV1 = "codefly.runnable-package.digest/v1"
	// BindingDigestFormatV1 is the binding counterpart of PackageDigestFormatV1.
	BindingDigestFormatV1 = "codefly.runnable-binding.digest/v1"
)

// CanonicalJSON returns the canonical proto3 JSON form core digests over: the
// proto3 JSON mapping of message with UseProtoNames, object keys sorted and
// whitespace removed. protojson deliberately varies its whitespace, so a
// consumer that has to reproduce these bytes — `codefly generate runnables`
// writes them to disk and diffs the committed file against a freshly derived
// one — calls this rather than marshalling a second time on its own.
//
// message must already be in canonical form. This normalizes the JSON
// encoding, never the message: the unordered sets a descriptor sorts are
// sorted by PreparePackage and PrepareBinding, and a message that has not been
// through one of those canonicalizes to stable bytes of an uncanonical
// descriptor — reproducible, and not what core digests.
//
// `<`, `>` and `&` are emitted as themselves rather than escaped to \uXXXX.
// Go's encoding/json escapes them by default; no other language's encoder
// does, and these bytes are reproduced outside Go. executionplan's
// marshalCompact makes the same choice for the same reason.
//
// The byte form is a contract, not only the digest taken over it: changing the
// normalization turns every committed file into spurious drift, everywhere at
// once, and every unchanged re-registration into ErrConflict.
//
// Unknown fields are rejected rather than dropped: a message carrying fields
// outside the schema it claims cannot be canonicalized by this version, and a
// form that ignored them would let those bytes change without notice.
func CanonicalJSON(message proto.Message) ([]byte, error) {
	// An absent message is a caller error, not an empty descriptor: a typed nil
	// canonicalizes to "{}" without complaint, and a consumer writing that to
	// disk gets a file that every later check happily agrees with.
	if message == nil || !message.ProtoReflect().IsValid() {
		return nil, fmt.Errorf("%w: message is required", ErrInvalid)
	}
	if carriesUnknownFields(message.ProtoReflect()) {
		return nil, fmt.Errorf("%w: message carries fields outside its declared schema", ErrInvalid)
	}
	encoded, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("%w: encode canonical form: %v", ErrInvalid, err)
	}
	var generic any
	if decodeErr := json.Unmarshal(encoded, &generic); decodeErr != nil {
		return nil, fmt.Errorf("%w: decode canonical form: %v", ErrInvalid, decodeErr)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err = encoder.Encode(generic); err != nil {
		return nil, fmt.Errorf("%w: encode canonical form: %v", ErrInvalid, err)
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}

// digestOf hashes the canonical form core owns. The protobuf wire encoding,
// even with Deterministic set, is only stable within one binary, and a release
// identity that changed with a library upgrade would turn every unchanged
// re-registration into a conflict.
func digestOf(format string, message proto.Message) (string, error) {
	canonical, err := CanonicalJSON(message)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	hash.Write([]byte(format))
	hash.Write([]byte{0})
	hash.Write(canonical)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// carriesUnknownFields walks every nested message, since unknown bytes on a
// nested message are as invisible to the JSON form as those on the root.
func carriesUnknownFields(message protoreflect.Message) bool {
	if len(message.GetUnknown()) > 0 {
		return true
	}
	found := false
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		switch {
		case field.IsList() && field.Kind() == protoreflect.MessageKind:
			list := value.List()
			for i := 0; i < list.Len() && !found; i++ {
				found = carriesUnknownFields(list.Get(i).Message())
			}
		case field.IsMap() && field.MapValue().Kind() == protoreflect.MessageKind:
			value.Map().Range(func(_ protoreflect.MapKey, entry protoreflect.Value) bool {
				found = carriesUnknownFields(entry.Message())
				return !found
			})
		case !field.IsList() && !field.IsMap() && field.Kind() == protoreflect.MessageKind:
			found = carriesUnknownFields(value.Message())
		}
		return !found
	})
	return found
}
