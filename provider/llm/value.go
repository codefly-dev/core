package llm

import providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"

func stringValue(value string) *providerv0.PublicValue {
	return &providerv0.PublicValue{Kind: &providerv0.PublicValue_StringValue{StringValue: value}}
}

func integerValue(value int64) *providerv0.PublicValue {
	return &providerv0.PublicValue{Kind: &providerv0.PublicValue_IntegerValue{IntegerValue: value}}
}

func boolValue(value bool) *providerv0.PublicValue {
	return &providerv0.PublicValue{Kind: &providerv0.PublicValue_BoolValue{BoolValue: value}}
}

func decimalValue(value string) *providerv0.PublicValue {
	return &providerv0.PublicValue{Kind: &providerv0.PublicValue_DecimalValue{DecimalValue: value}}
}

func listValue(values []*providerv0.PublicValue) *providerv0.PublicValue {
	return &providerv0.PublicValue{Kind: &providerv0.PublicValue_ListValue{ListValue: &providerv0.PublicList{Values: values}}}
}

func objectValue(fields map[string]*providerv0.PublicValue) *providerv0.PublicValue {
	return &providerv0.PublicValue{Kind: &providerv0.PublicValue_ObjectValue{ObjectValue: &providerv0.PublicObject{Fields: fields}}}
}
