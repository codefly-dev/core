package llm

import providerv1 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v1"

func stringValue(value string) *providerv1.PublicValue {
	return &providerv1.PublicValue{Kind: &providerv1.PublicValue_StringValue{StringValue: value}}
}

func integerValue(value int64) *providerv1.PublicValue {
	return &providerv1.PublicValue{Kind: &providerv1.PublicValue_IntegerValue{IntegerValue: value}}
}

func boolValue(value bool) *providerv1.PublicValue {
	return &providerv1.PublicValue{Kind: &providerv1.PublicValue_BoolValue{BoolValue: value}}
}

func decimalValue(value string) *providerv1.PublicValue {
	return &providerv1.PublicValue{Kind: &providerv1.PublicValue_DecimalValue{DecimalValue: value}}
}

func listValue(values []*providerv1.PublicValue) *providerv1.PublicValue {
	return &providerv1.PublicValue{Kind: &providerv1.PublicValue_ListValue{ListValue: &providerv1.PublicList{Values: values}}}
}

func objectValue(fields map[string]*providerv1.PublicValue) *providerv1.PublicValue {
	return &providerv1.PublicValue{Kind: &providerv1.PublicValue_ObjectValue{ObjectValue: &providerv1.PublicObject{Fields: fields}}}
}
