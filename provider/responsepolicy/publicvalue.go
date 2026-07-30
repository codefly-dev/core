package responsepolicy

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/configuration"
)

// toPublicValue converts a parsed JSON value into a normalized PublicValue.
// Every string is checked against the secret heuristic: a FORWARD_SAFE field
// that carries a secret-shaped literal is drift and fails closed rather than
// forwarding the bytes.
func toPublicValue(value any) (*providerv0.PublicValue, error) {
	switch v := value.(type) {
	case nil:
		return &providerv0.PublicValue{Kind: &providerv0.PublicValue_NullValue{NullValue: true}}, nil
	case bool:
		return &providerv0.PublicValue{Kind: &providerv0.PublicValue_BoolValue{BoolValue: v}}, nil
	case string:
		if configuration.LooksSecret(v) {
			return nil, fmt.Errorf("forwarded field carries a secret-shaped literal")
		}
		return &providerv0.PublicValue{Kind: &providerv0.PublicValue_StringValue{StringValue: v}}, nil
	case json.Number:
		return numberToPublicValue(v)
	case []any:
		list := &providerv0.PublicList{Values: make([]*providerv0.PublicValue, 0, len(v))}
		for _, item := range v {
			converted, err := toPublicValue(item)
			if err != nil {
				return nil, err
			}
			list.Values = append(list.Values, converted)
		}
		return &providerv0.PublicValue{Kind: &providerv0.PublicValue_ListValue{ListValue: list}}, nil
	case map[string]any:
		object := &providerv0.PublicObject{Fields: make(map[string]*providerv0.PublicValue, len(v))}
		keys := make([]string, 0, len(v))
		for key := range v {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			converted, err := toPublicValue(v[key])
			if err != nil {
				return nil, err
			}
			object.Fields[key] = converted
		}
		return &providerv0.PublicValue{Kind: &providerv0.PublicValue_ObjectValue{ObjectValue: object}}, nil
	default:
		return nil, fmt.Errorf("unsupported response value type %T", value)
	}
}

// numberToPublicValue renders a JSON number as either a signed integer or a
// canonical base-ten decimal. Exponent forms and non-canonical spellings fail
// closed so a forwarded number has exactly one representation.
func numberToPublicValue(number json.Number) (*providerv0.PublicValue, error) {
	raw := string(number)
	if integer, err := strconv.ParseInt(raw, 10, 64); err == nil && strconv.FormatInt(integer, 10) == raw {
		return &providerv0.PublicValue{Kind: &providerv0.PublicValue_IntegerValue{IntegerValue: integer}}, nil
	}
	decimal, err := canonicalDecimal(raw)
	if err != nil {
		return nil, err
	}
	return &providerv0.PublicValue{Kind: &providerv0.PublicValue_DecimalValue{DecimalValue: decimal}}, nil
}

// canonicalDecimal normalizes a plain decimal string to the canonical form
// consumed by the canonical package: no exponent, no leading zeros, no
// insignificant trailing fraction zeros, and no negative zero.
func canonicalDecimal(raw string) (string, error) {
	if strings.ContainsAny(raw, "eE") {
		return "", fmt.Errorf("decimal must not use exponent notation")
	}
	negative := strings.HasPrefix(raw, "-")
	digits := strings.TrimPrefix(raw, "-")
	integerPart, fractionPart, hasFraction := strings.Cut(digits, ".")
	if integerPart == "" || !isDigits(integerPart) || (hasFraction && !isDigits(fractionPart)) {
		return "", fmt.Errorf("decimal %q is not canonical", raw)
	}
	integerPart = strings.TrimLeft(integerPart, "0")
	if integerPart == "" {
		integerPart = "0"
	}
	fractionPart = strings.TrimRight(fractionPart, "0")
	result := integerPart
	if fractionPart != "" {
		result += "." + fractionPart
	}
	if negative && result != "0" {
		result = "-" + result
	}
	return result, nil
}

func isDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, r := range value {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
