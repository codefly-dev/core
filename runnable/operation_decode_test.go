package runnable_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/codefly-dev/core/runnable"
)

// A policy round-trips through its canonical document, and a document carrying
// a field this reader does not declare is refused rather than read without it.
// That refusal is the whole of what an older reader can do about a newer
// requirement: the field it does not know is the requirement, and dropping it
// would deliver an authority the owner never declared as complete.
func TestDecodeOperationRefusesWhatItDoesNotDeclare(t *testing.T) {
	declared := slottedOperation()
	value, err := runnable.EncodeOperation(declared)
	require.NoError(t, err)
	read, err := runnable.DecodeOperation(value)
	require.NoError(t, err)
	require.True(t, proto.Equal(declared, read))
	require.Len(t, read.GetRequiredScopeSlots(), 2)

	// The document as a reader on an older schema would see it: one field it
	// has no name for. The newer field stands in for any newer field.
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(value, &fields))
	fields["required_scope_slots_from_a_future_schema"] = fields["required_scope_slots"]
	delete(fields, "required_scope_slots")
	foreign, err := json.Marshal(fields)
	require.NoError(t, err)
	_, err = runnable.DecodeOperation(foreign)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.Contains(t, err.Error(), "required_scope_slots_from_a_future_schema")

	// A lenient reader is exactly the failure: it reads the same bytes as a
	// complete, slot-free policy and nothing downstream can tell.
	lenient, err := runnable.DecodeOperation(func() []byte {
		delete(fields, "required_scope_slots_from_a_future_schema")
		stripped, err := json.Marshal(fields)
		require.NoError(t, err)
		return stripped
	}())
	require.NoError(t, err)
	require.Empty(t, lenient.GetRequiredScopeSlots())

	_, err = runnable.DecodeOperation(nil)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "outside (0,")
	// The bound is on the bytes, before anything is parsed: a document larger
	// than a prepared binding may be is refused as a size, not as a syntax.
	oversize := []byte(`{"audience":"` + strings.Repeat("a", runnable.MaxPreparedBytes) + `"}`)
	_, err = runnable.DecodeOperation(oversize)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "outside (0,")
	_, err = runnable.EncodeOperation(nil)
	require.ErrorIs(t, err, runnable.ErrInvalid)
}
