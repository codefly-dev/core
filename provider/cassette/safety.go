package cassette

import (
	"fmt"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/configuration"
	"google.golang.org/protobuf/reflect/protoreflect"
)

// assertSafe walks a filtered response and rejects any secret-shaped string. A
// forwarded value, selector, suppressed presence marker, or capture reference
// that still looks like a secret means filtering drifted, and the response must
// not be recorded.
func assertSafe(response *providerv0.ExecuteRequestResponse) error {
	if response == nil {
		return fmt.Errorf("cassette response is required")
	}
	return scanMessage(response.ProtoReflect())
}

func scanMessage(message protoreflect.Message) error {
	var found error
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		found = scanValue(field, value)
		return found == nil
	})
	return found
}

func scanValue(field protoreflect.FieldDescriptor, value protoreflect.Value) error {
	switch {
	case field.IsMap():
		return scanMap(field, value)
	case field.IsList():
		return scanList(field, value)
	default:
		return scanScalar(field, value)
	}
}

func scanMap(field protoreflect.FieldDescriptor, value protoreflect.Value) error {
	var found error
	value.Map().Range(func(key protoreflect.MapKey, entry protoreflect.Value) bool {
		if configuration.LooksSecret(key.String()) {
			found = fmt.Errorf("map key is secret-shaped")
			return false
		}
		if field.MapValue().Kind() == protoreflect.MessageKind {
			found = scanMessage(entry.Message())
		} else if field.MapValue().Kind() == protoreflect.StringKind && configuration.LooksSecret(entry.String()) {
			found = fmt.Errorf("map value is secret-shaped")
		}
		return found == nil
	})
	return found
}

func scanList(field protoreflect.FieldDescriptor, value protoreflect.Value) error {
	list := value.List()
	for i := 0; i < list.Len(); i++ {
		if field.Kind() == protoreflect.MessageKind {
			if err := scanMessage(list.Get(i).Message()); err != nil {
				return err
			}
		} else if field.Kind() == protoreflect.StringKind && configuration.LooksSecret(list.Get(i).String()) {
			return fmt.Errorf("repeated value is secret-shaped")
		}
	}
	return nil
}

func scanScalar(field protoreflect.FieldDescriptor, value protoreflect.Value) error {
	switch field.Kind() {
	case protoreflect.MessageKind:
		return scanMessage(value.Message())
	case protoreflect.StringKind:
		if configuration.LooksSecret(value.String()) {
			return fmt.Errorf("field %q is secret-shaped", field.Name())
		}
	}
	return nil
}
