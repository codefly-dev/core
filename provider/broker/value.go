package broker

import (
	"fmt"
	"strconv"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/configuration"
)

// scalarString renders a PublicValue as a path or query scalar. Only string,
// integer, decimal, and boolean values are admitted; a secret-shaped string
// fails closed so provider input cannot smuggle a credential into a URL.
func scalarString(value *providerv0.PublicValue) (string, error) {
	switch kind := value.GetKind().(type) {
	case *providerv0.PublicValue_StringValue:
		if configuration.LooksSecret(kind.StringValue) {
			return "", fmt.Errorf("value carries a secret-shaped literal")
		}
		return kind.StringValue, nil
	case *providerv0.PublicValue_IntegerValue:
		return strconv.FormatInt(kind.IntegerValue, 10), nil
	case *providerv0.PublicValue_DecimalValue:
		return kind.DecimalValue, nil
	case *providerv0.PublicValue_BoolValue:
		return strconv.FormatBool(kind.BoolValue), nil
	default:
		return "", fmt.Errorf("value is not a scalar")
	}
}

// nativeValue converts a PublicValue tree into a JSON-encodable Go value for
// request body serialization. Every string is checked so a secret cannot be
// placed in an outbound body by provider code.
func nativeValue(value *providerv0.PublicValue) (any, error) {
	switch kind := value.GetKind().(type) {
	case *providerv0.PublicValue_NullValue:
		return nil, nil
	case *providerv0.PublicValue_StringValue:
		if configuration.LooksSecret(kind.StringValue) {
			return nil, fmt.Errorf("value carries a secret-shaped literal")
		}
		return kind.StringValue, nil
	case *providerv0.PublicValue_BoolValue:
		return kind.BoolValue, nil
	case *providerv0.PublicValue_IntegerValue:
		return kind.IntegerValue, nil
	case *providerv0.PublicValue_DecimalValue:
		return jsonNumber(kind.DecimalValue), nil
	case *providerv0.PublicValue_ListValue:
		list := make([]any, 0, len(kind.ListValue.GetValues()))
		for _, item := range kind.ListValue.GetValues() {
			converted, err := nativeValue(item)
			if err != nil {
				return nil, err
			}
			list = append(list, converted)
		}
		return list, nil
	case *providerv0.PublicValue_ObjectValue:
		object := make(map[string]any, len(kind.ObjectValue.GetFields()))
		for key, item := range kind.ObjectValue.GetFields() {
			converted, err := nativeValue(item)
			if err != nil {
				return nil, err
			}
			object[key] = converted
		}
		return object, nil
	default:
		return nil, fmt.Errorf("value kind is unset")
	}
}

// jsonNumber marshals as a bare JSON number rather than a quoted string.
type jsonNumber string

func (n jsonNumber) MarshalJSON() ([]byte, error) {
	return []byte(n), nil
}
