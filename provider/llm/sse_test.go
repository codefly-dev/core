package llm_test

import (
	"fmt"
	"strings"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
)

// reconstructSSE re-emits filtered events as a well-formed Server-Sent Events
// body for framing-fidelity assertions. Each event becomes an `event:`/`data:`
// frame carrying the safe projection of its forwarded fields, in the fields'
// deterministic emission order (not a re-sort, which would misorder array
// indices ≥ 10), so record and replay reconstruct byte-identical framing.
func reconstructSSE(events []*providerv0.FilteredEvent) []byte {
	var builder strings.Builder
	for _, event := range events {
		if event.GetEventType() != "" {
			fmt.Fprintf(&builder, "event: %s\n", event.GetEventType())
		}
		builder.WriteString("data: ")
		builder.WriteString(safeProjection(event.GetForwarded()))
		builder.WriteString("\n\n")
	}
	return []byte(builder.String())
}

func safeProjection(fields []*providerv0.FilteredField) string {
	pairs := make([]string, 0, len(fields))
	for _, field := range fields {
		pairs = append(pairs, fmt.Sprintf("%q:%s", field.GetSelector(), scalarJSON(field.GetValue())))
	}
	return "{" + strings.Join(pairs, ",") + "}"
}

func scalarJSON(value *providerv0.PublicValue) string {
	switch kind := value.GetKind().(type) {
	case *providerv0.PublicValue_StringValue:
		return fmt.Sprintf("%q", kind.StringValue)
	case *providerv0.PublicValue_IntegerValue:
		return fmt.Sprintf("%d", kind.IntegerValue)
	case *providerv0.PublicValue_DecimalValue:
		return kind.DecimalValue
	case *providerv0.PublicValue_BoolValue:
		return fmt.Sprintf("%t", kind.BoolValue)
	default:
		return "null"
	}
}
