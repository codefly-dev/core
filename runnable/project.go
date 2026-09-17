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

// wellKnownPrefix names the protobuf well-known types. They carry meaning the
// bounded profile does not have — a Timestamp is not a string and an Any is
// not an object — so projecting one would invent a representation the runtime
// never agreed to.
const wellKnownPrefix = "google.protobuf."

// ProjectMessage projects a message descriptor onto the bounded schema
// profile. It is the one implementation of that mapping: a derived operation's
// contract and the owner's published message are the same shape by
// construction, never by two readings of the same .proto agreeing.
//
// A type outside the profile is rejected, named by its full field path, and is
// never coerced. The payload is fixed once in the .proto and stays fixed,
// which is why this fails generation rather than an invocation.
func ProjectMessage(md protoreflect.MessageDescriptor) (*basev0.RunnableSchema, error) {
	if md == nil {
		return nil, fmt.Errorf("%w: message descriptor is required", ErrInvalid)
	}
	fields, err := projectFields(md, string(md.FullName()), nil)
	if err != nil {
		return nil, err
	}
	return &basev0.RunnableSchema{Fields: fields}, nil
}

// projectFields walks one message. enclosing is the chain of messages already
// open, so it carries both the depth and the cycle check: a message that
// reaches itself describes an unbounded payload, not a deep one.
func projectFields(md protoreflect.MessageDescriptor, at string, enclosing []protoreflect.FullName) ([]*basev0.RunnableField, error) {
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
		field, err := projectField(descriptors.Get(i), at, enclosing)
		if err != nil {
			return nil, err
		}
		fields = append(fields, field)
	}
	return fields, nil
}

func projectField(fd protoreflect.FieldDescriptor, parent string, enclosing []protoreflect.FullName) (*basev0.RunnableField, error) {
	at := parent + "." + string(fd.Name())
	// A proto3 optional field is a synthetic one-member oneof; only an authored
	// oneof is a choice the profile has no shape for.
	if oneof := fd.ContainingOneof(); oneof != nil && !oneof.IsSynthetic() {
		return nil, fmt.Errorf("%w: %s belongs to oneof %s, which the bounded profile does not cover", ErrInvalid, at, oneof.Name())
	}
	if fd.IsMap() {
		return nil, fmt.Errorf("%w: %s is a map, which the bounded profile does not cover", ErrInvalid, at)
	}
	value := &basev0.RunnableField{}
	switch fd.Kind() {
	case protoreflect.StringKind:
		value.Type = basev0.RunnableField_STRING
	case protoreflect.BoolKind:
		value.Type = basev0.RunnableField_BOOLEAN
	case protoreflect.Int32Kind, protoreflect.Sint32Kind, protoreflect.Sfixed32Kind,
		protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint32Kind, protoreflect.Fixed32Kind:
		value.Type = basev0.RunnableField_INTEGER
	case protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		return nil, fmt.Errorf("%w: %s is %s, whose range exceeds the signed 64-bit integer the bounded profile carries", ErrInvalid, at, fd.Kind())
	case protoreflect.MessageKind:
		if strings.HasPrefix(string(fd.Message().FullName()), wellKnownPrefix) {
			return nil, fmt.Errorf("%w: %s is %s, a well-known type the bounded profile has no representation for", ErrInvalid, at, fd.Message().FullName())
		}
		fields, err := projectFields(fd.Message(), at, enclosing)
		if err != nil {
			return nil, err
		}
		value.Type, value.Fields = basev0.RunnableField_OBJECT, fields
	default:
		return nil, fmt.Errorf("%w: %s is %s, which the bounded profile does not cover", ErrInvalid, at, fd.Kind())
	}
	if fd.IsList() {
		// An element is present or the list is shorter, so the item carries
		// neither the field's name nor its optionality.
		return &basev0.RunnableField{Name: string(fd.Name()), Type: basev0.RunnableField_ARRAY, Items: value}, nil
	}
	value.Name = string(fd.Name())
	value.Optional = fd.HasPresence()
	return value, nil
}
