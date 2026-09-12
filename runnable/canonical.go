package runnable

import (
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

// digestOf hashes a canonical form core owns: the proto3 JSON mapping of the
// message with object keys sorted and whitespace removed. The protobuf wire
// encoding, even with Deterministic set, is only stable within one binary, and
// a release identity that changed with a library upgrade would turn every
// unchanged re-registration into a conflict.
//
// Unknown fields are rejected rather than dropped: a message carrying fields
// outside the schema it claims cannot be canonicalized by this version, and a
// digest that ignored them would let those bytes change without notice.
func digestOf(format string, message proto.Message) (string, error) {
	if carriesUnknownFields(message.ProtoReflect()) {
		return "", fmt.Errorf("%w: message carries fields outside its declared schema", ErrInvalid)
	}
	encoded, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(message)
	if err != nil {
		return "", fmt.Errorf("%w: encode canonical form: %v", ErrInvalid, err)
	}
	var generic any
	if decodeErr := json.Unmarshal(encoded, &generic); decodeErr != nil {
		return "", fmt.Errorf("%w: decode canonical form: %v", ErrInvalid, decodeErr)
	}
	canonical, err := json.Marshal(generic)
	if err != nil {
		return "", fmt.Errorf("%w: encode canonical form: %v", ErrInvalid, err)
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
