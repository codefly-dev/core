package runnable_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	_ "github.com/codefly-dev/core/generated/go/codefly/runnable/receipts/v0"
)

// The generic receipt lookup is what an operation declaring no lookup_method
// falls back to, so it sits beside the validation that permits that empty
// declaration. The Go SDK, the runtime's generic adapter and the Python SDK
// encode against this schema rather than against each other.
//
// What this pins is the descriptor the bindings embed — what every Go consumer
// imports — and not receipts.proto itself. internal/protoguard is what holds
// the two together: it compiles the schema with protocompile and compares the
// result against these same registered descriptors, so a schema edit shipped
// without regenerating fails there rather than here.
func TestReceiptsLookupIsTheGenericWireContract(t *testing.T) {
	fieldNumbers := func(message protoreflect.MessageDescriptor) map[string]protoreflect.FieldNumber {
		numbers := make(map[string]protoreflect.FieldNumber, message.Fields().Len())
		for i := range message.Fields().Len() {
			field := message.Fields().Get(i)
			numbers[string(field.Name())] = field.Number()
		}
		return numbers
	}

	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName("codefly.runnable.receipts.v0.Receipts")
	require.NoError(t, err)
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	require.True(t, ok)

	require.Equal(t, 1, service.Methods().Len())
	lookup := service.Methods().Get(0)
	require.Equal(t, protoreflect.Name("Lookup"), lookup.Name())
	require.False(t, lookup.IsStreamingClient())
	require.False(t, lookup.IsStreamingServer())

	// The tenant is absent by construction: it comes from the verified Work
	// Context, so a tenant field here would let one tenant name another's
	// effect. Comparing the whole map is what makes adding one fail.
	require.Equal(t, map[string]protoreflect.FieldNumber{
		"effect_id": 1,
		"method":    2,
	}, fieldNumbers(lookup.Input()))

	require.Equal(t, map[string]protoreflect.FieldNumber{
		"response": 1,
		"status":   2,
	}, fieldNumbers(lookup.Output()))
	require.Equal(t, protoreflect.BytesKind, lookup.Output().Fields().ByName("response").Kind())
	require.Equal(t, protoreflect.FullName("google.rpc.Status"),
		lookup.Output().Fields().ByName("status").Message().FullName())
}

// A receipt records one terminal outcome, and an operation that durably
// commits a rejection has one just as much as one that commits a payload.
// Without the status arm the only answers available are an empty response,
// which decodes as a zero-valued success the effect never returned, and
// NOT_FOUND, which the contract defines as inconclusive — so the recovery
// re-runs the effect the receipt exists to keep from running twice.
//
// The oneof is what makes both unrepresentable, so the oneof is what is
// asserted: two arms, mutually exclusive, and a set-but-empty response
// distinguishable from no outcome at all.
func TestReceiptLookupCanCarryACommittedRejection(t *testing.T) {
	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName("codefly.runnable.receipts.v0.LookupResponse")
	require.NoError(t, err)
	response, ok := descriptor.(protoreflect.MessageDescriptor)
	require.True(t, ok)

	require.Equal(t, 1, response.Oneofs().Len())
	outcome := response.Oneofs().Get(0)
	require.Equal(t, protoreflect.Name("outcome"), outcome.Name())
	require.False(t, outcome.IsSynthetic(), "a synthetic oneof is optional-field presence, not a choice between arms")

	arms := make([]string, 0, outcome.Fields().Len())
	for i := range outcome.Fields().Len() {
		arms = append(arms, string(outcome.Fields().Get(i).Name()))
	}
	require.Equal(t, []string{"response", "status"}, arms)
}
