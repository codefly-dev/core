package runnable_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	_ "github.com/codefly-dev/core/generated/go/codefly/runnable/receipts/v0"
)

// The generic receipt lookup is what an operation declaring no lookup_method
// falls back to, and three implementations — the Go SDK, the runtime's generic
// adapter, and the Python SDK — encode against this schema rather than against
// each other. The shape below is therefore the contract itself: the field
// numbers a receipt is asked for by, and the absence of a tenant field, which
// comes from the verified Work Context so that one tenant cannot name another's
// effect.
func TestReceiptsLookupIsTheGenericWireContract(t *testing.T) {
	descriptor, err := protoregistry.GlobalFiles.FindDescriptorByName("codefly.runnable.receipts.v0.Receipts")
	require.NoError(t, err)
	service, ok := descriptor.(protoreflect.ServiceDescriptor)
	require.True(t, ok)

	require.Equal(t, 1, service.Methods().Len())
	lookup := service.Methods().Get(0)
	require.Equal(t, protoreflect.Name("Lookup"), lookup.Name())
	require.False(t, lookup.IsStreamingClient())
	require.False(t, lookup.IsStreamingServer())

	require.Equal(t, map[string]protoreflect.FieldNumber{
		"effect_id": 1,
		"method":    2,
	}, fieldNumbers(lookup.Input()))
	require.Equal(t, map[string]protoreflect.FieldNumber{
		"response": 1,
	}, fieldNumbers(lookup.Output()))
	require.Equal(t, protoreflect.BytesKind, lookup.Output().Fields().ByName("response").Kind())
}

func fieldNumbers(message protoreflect.MessageDescriptor) map[string]protoreflect.FieldNumber {
	numbers := make(map[string]protoreflect.FieldNumber, message.Fields().Len())
	for i := range message.Fields().Len() {
		field := message.Fields().Get(i)
		numbers[string(field.Name())] = field.Number()
	}
	return numbers
}
