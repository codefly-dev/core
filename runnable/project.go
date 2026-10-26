package runnable

import (
	"fmt"
	"slices"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// MaxProjectionDepth bounds how many messages a projected payload may nest.
// The bound exists so a deeply nested owner message is a generation failure
// with a path to fix rather than a schema no generated binding can express.
const MaxProjectionDepth = 32

// MaxProjectionFields bounds how many fields a projected payload may carry in
// total. Depth alone does not bound the work: a payload whose members are
// themselves objects expands multiplicatively, so a .proto or a document of a
// couple of kilobytes can describe a payload of tens of megabytes, and one
// twice as deep exhausts memory before it finishes. The bound is on the
// projection rather than on the source, because the source is small either way.
const MaxProjectionFields = 10000

// wellKnownPrefix names the protobuf well-known types. Each one has its own
// proto3 JSON spelling — a Timestamp is an RFC 3339 string, not the two
// integers its descriptor declares — so a well-known type is projected by
// that spelling (wellKnown), never by walking its fields.
const wellKnownPrefix = "google.protobuf."

// The formats a projected value carries: the spellings proto3 JSON gives a
// value whose type alone does not say how it is written. The set is closed
// (resources.RunnableFieldFormats) so a format is a fact, never a hint, and
// the protobuf-specific ones say so in their name: proto3 JSON writes a
// Duration as "1.5s", which JSON Schema's ISO 8601 "duration" would refuse.
const (
	FormatDateTime         = "date-time"
	FormatProtobufDuration = "protobuf-duration"
	FormatFieldMask        = "protobuf-field-mask"
	FormatByte             = "byte"
	FormatInt64            = "int64"
	FormatUint64           = "uint64"
	FormatObject           = "object"
	FormatArray            = "array"
)

// emptyPayload is the one well-known type a payload may be: proto3 JSON
// carries google.protobuf.Empty as an object with no keys, which is exactly
// the contract its projection states.
const emptyPayload = "google.protobuf.Empty"

// ProjectMessage projects a message descriptor onto the bounded schema
// profile. It is the one implementation of that mapping: a derived operation's
// contract and the owner's published message are the same shape by
// construction, never by two readings of the same .proto agreeing.
//
// The profile describes the JSON the owner's Connect endpoint reads and
// writes, so every type is projected by its canonical proto3 JSON mapping: a
// Timestamp is a "date-time" string, a Duration a "protobuf-duration" string, an enum
// a string naming one of its values, bytes a "byte" string, a float or double
// a NUMBER, a 64-bit integer an "int64" or "uint64" decimal string, a Struct, Value,
// ListValue or map an arbitrary JSON value, a wrapper its wrapped value made
// nullable, and an authored oneof's members optional keys. Every member is
// named by its JSON name (lowerCamelCase, or the json_name the owner chose),
// which is the key a Connect server writes; protojson reads either spelling,
// so the input loses nothing. What has no
// canonical JSON a caller can write without the owner's type registry —
// google.protobuf.Any — and what has no bounded shape — a recursive message —
// is rejected, named by its full field path, and is never coerced.
func ProjectMessage(md protoreflect.MessageDescriptor) (*basev0.RunnableSchema, error) {
	if md == nil {
		return nil, fmt.Errorf("%w: message descriptor is required", ErrInvalid)
	}
	// A payload is a JSON object. Empty is one with no keys; every other
	// well-known type is carried as a string, a number or an arbitrary value,
	// which is not an object whose keys the contract can name.
	if name := string(md.FullName()); strings.HasPrefix(name, wellKnownPrefix) && name != emptyPayload {
		return nil, fmt.Errorf("%w: payload %s is a well-known type, which proto3 JSON does not carry as an object with named keys", ErrInvalid, md.FullName())
	}
	budget := MaxProjectionFields
	fields, err := projectFields(md, string(md.FullName()), nil, &budget)
	if err != nil {
		return nil, err
	}
	return &basev0.RunnableSchema{Fields: fields}, nil
}

// projectFields walks one message. enclosing is the chain of messages already
// open, so it carries both the depth and the cycle check: a message that
// reaches itself describes an unbounded payload, not a deep one.
func projectFields(md protoreflect.MessageDescriptor, at string, enclosing []protoreflect.FullName, budget *int) ([]*basev0.RunnableField, error) {
	if slices.Contains(enclosing, md.FullName()) {
		return nil, fmt.Errorf("%w: %s reaches %s again, and a recursive message has no bounded shape", ErrInvalid, at, md.FullName())
	}
	if len(enclosing) >= MaxProjectionDepth {
		return nil, fmt.Errorf("%w: %s nests more than %d messages deep", ErrInvalid, at, MaxProjectionDepth)
	}
	enclosing = append(enclosing, md.FullName())
	descriptors := md.Fields()
	fields := make([]*basev0.RunnableField, 0, descriptors.Len())
	for i := 0; i < descriptors.Len(); i++ {
		if *budget--; *budget < 0 {
			return nil, fmt.Errorf("%w: %s projects more than %d fields; depth alone does not bound a message whose fields are themselves messages", ErrInvalid, at, MaxProjectionFields)
		}
		field, err := projectField(descriptors.Get(i), at, enclosing, budget)
		if err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func projectField(fd protoreflect.FieldDescriptor, parent string, enclosing []protoreflect.FullName, budget *int) (*basev0.RunnableField, error) {
	at := parent + "." + string(fd.Name())
	if fd.IsMap() {
		// proto3 JSON carries a map as an object keyed by the map's keys, which
		// the owner does not enumerate.
		return &basev0.RunnableField{Name: fd.JSONName(), Type: basev0.RunnableField_JSON, Format: FormatObject, Optional: true}, nil
	}
	value, err := projectValue(fd, at, enclosing, budget)
	if err != nil {
		return nil, err
	}
	if fd.IsList() {
		// An element is present or the list is shorter, so the item carries
		// neither the field's name nor its optionality.
		return &basev0.RunnableField{Name: fd.JSONName(), Type: basev0.RunnableField_ARRAY, Items: value}, nil
	}
	value.Name = fd.JSONName()
	// A required field tracks presence but its key is never absent, so reading
	// presence alone would mark proto2 required and editions LEGACY_REQUIRED
	// fields omissible — the inverse of what they declare. A member of an
	// authored oneof is its own key, and at most one of them is present, so
	// each one is optional.
	value.Optional = fd.HasPresence() && fd.Cardinality() != protoreflect.Required
	return value, nil
}

// projectValue projects one value of a field: an element of a list, or the
// field itself.
func projectValue(fd protoreflect.FieldDescriptor, at string, enclosing []protoreflect.FullName, budget *int) (*basev0.RunnableField, error) {
	switch fd.Kind() {
	case protoreflect.StringKind:
		return &basev0.RunnableField{Type: basev0.RunnableField_STRING}, nil
	case protoreflect.BoolKind:
		return &basev0.RunnableField{Type: basev0.RunnableField_BOOLEAN}, nil
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		return &basev0.RunnableField{Type: basev0.RunnableField_INTEGER}, nil
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind:
		// proto3 JSON writes a 64-bit integer as a decimal string, so that is
		// what the contract states: an answer arrives as "7", never 7.
		return &basev0.RunnableField{Type: basev0.RunnableField_STRING, Format: FormatInt64}, nil
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return &basev0.RunnableField{Type: basev0.RunnableField_STRING, Format: FormatUint64}, nil
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		// proto3 JSON writes a non-finite value as "NaN", "Infinity" or
		// "-Infinity"; a NUMBER admits those three strings (RunnableField).
		return &basev0.RunnableField{Type: basev0.RunnableField_NUMBER}, nil
	case protoreflect.BytesKind:
		return &basev0.RunnableField{Type: basev0.RunnableField_STRING, Format: FormatByte}, nil
	case protoreflect.EnumKind:
		return enumValue(fd.Enum()), nil
	case protoreflect.MessageKind, protoreflect.GroupKind:
		if strings.HasPrefix(string(fd.Message().FullName()), wellKnownPrefix) {
			return wellKnown(fd.Message(), at)
		}
		fields, err := projectFields(fd.Message(), at, enclosing, budget)
		if err != nil {
			return nil, err
		}
		return &basev0.RunnableField{Type: basev0.RunnableField_OBJECT, Fields: fields}, nil
	default:
		return nil, fmt.Errorf("%w: %s is %s, which the bounded profile does not cover", ErrInvalid, at, fd.Kind())
	}
}

// enumValue is an enum as proto3 JSON writes it: by the name of its value.
// google.protobuf.NullValue is the exception, carried as JSON null.
func enumValue(ed protoreflect.EnumDescriptor) *basev0.RunnableField {
	if ed.FullName() == "google.protobuf.NullValue" {
		return &basev0.RunnableField{Type: basev0.RunnableField_JSON, Nullable: true}
	}
	values := make([]string, 0, ed.Values().Len())
	for i := 0; i < ed.Values().Len(); i++ {
		values = append(values, string(ed.Values().Get(i).Name()))
	}
	return &basev0.RunnableField{Type: basev0.RunnableField_STRING, Values: values}
}

// wellKnown projects a google.protobuf type by its proto3 JSON spelling.
func wellKnown(md protoreflect.MessageDescriptor, at string) (*basev0.RunnableField, error) {
	switch md.FullName() {
	case "google.protobuf.Timestamp":
		return &basev0.RunnableField{Type: basev0.RunnableField_STRING, Format: FormatDateTime}, nil
	case "google.protobuf.Duration":
		return &basev0.RunnableField{Type: basev0.RunnableField_STRING, Format: FormatProtobufDuration}, nil
	case "google.protobuf.FieldMask":
		return &basev0.RunnableField{Type: basev0.RunnableField_STRING, Format: FormatFieldMask}, nil
	case "google.protobuf.Empty":
		return &basev0.RunnableField{Type: basev0.RunnableField_OBJECT}, nil
	case "google.protobuf.Struct":
		return &basev0.RunnableField{Type: basev0.RunnableField_JSON, Format: FormatObject}, nil
	case "google.protobuf.ListValue":
		return &basev0.RunnableField{Type: basev0.RunnableField_JSON, Format: FormatArray}, nil
	case "google.protobuf.Value":
		return &basev0.RunnableField{Type: basev0.RunnableField_JSON, Nullable: true}, nil
	case "google.protobuf.StringValue":
		return &basev0.RunnableField{Type: basev0.RunnableField_STRING, Nullable: true}, nil
	case "google.protobuf.BytesValue":
		return &basev0.RunnableField{Type: basev0.RunnableField_STRING, Format: FormatByte, Nullable: true}, nil
	case "google.protobuf.BoolValue":
		return &basev0.RunnableField{Type: basev0.RunnableField_BOOLEAN, Nullable: true}, nil
	case "google.protobuf.Int32Value", "google.protobuf.UInt32Value":
		return &basev0.RunnableField{Type: basev0.RunnableField_INTEGER, Nullable: true}, nil
	case "google.protobuf.Int64Value":
		return &basev0.RunnableField{Type: basev0.RunnableField_STRING, Format: FormatInt64, Nullable: true}, nil
	case "google.protobuf.UInt64Value":
		return &basev0.RunnableField{Type: basev0.RunnableField_STRING, Format: FormatUint64, Nullable: true}, nil
	case "google.protobuf.FloatValue", "google.protobuf.DoubleValue":
		return &basev0.RunnableField{Type: basev0.RunnableField_NUMBER, Nullable: true}, nil
	}
	return nil, fmt.Errorf("%w: %s is %s, a well-known type whose proto3 JSON a caller cannot write without the owner's type registry", ErrInvalid, at, md.FullName())
}
