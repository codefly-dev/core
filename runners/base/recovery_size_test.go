package base

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Serialization fixtures exercise the exact preview-construction seam. They
// name no live processes and grant no authority to execute recovery.
func recoveryReceiptSizeFixture(count int) (*TrackedProcessGroup, recordSnapshot, []processIdentity) {
	const boot = "7CA8814F-130E-4F30-A0E9-14064ED54319"
	leader := recordedProcessIdentity{PID: 12345, BootID: boot, StartID: 1793147946098482, Executable: "worker"}
	owner := recordedProcessIdentity{PID: 11234, BootID: boot, StartID: 1793147904561399, Executable: "owner"}
	group := &TrackedProcessGroup{namespace: pgidRegistryNamespace, record: pgidRecord{PGID: leader.PID, Leader: leader, Owner: owner, Authentication: strings.Repeat("a", groupAuthBytes*2)}}
	members := make([]processIdentity, count)
	for i := range members {
		members[i] = processIdentity{pid: leader.PID + i, pgid: leader.PID, bootID: boot, startID: leader.StartID + uint64(i), executable: "worker"}
	}
	return group, recordSnapshot{digest: strings.Repeat("b", 64)}, members
}

func encodeRecoveryReceipt(t *testing.T, expected *ProcessGroupRecoveryExpectation) []byte {
	t.Helper()
	var output bytes.Buffer
	require.NoError(t, json.NewEncoder(&output).Encode(expected))
	require.True(t, bytes.HasSuffix(output.Bytes(), []byte("\n")))
	return output.Bytes()
}

func TestRecoveryReceiptLargeGroupRoundTrip(t *testing.T) {
	for _, count := range []int{150, 160} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			group, snapshot, members := recoveryReceiptSizeFixture(count)
			expected, err := recoveryPreviewExpectation(group, snapshot, members)
			require.NoError(t, err)
			output := encodeRecoveryReceipt(t, expected)
			require.Greater(t, len(output), maxRecordSize)
			actual, err := ParseProcessGroupRecoveryExpectation(output)
			require.NoError(t, err)
			require.Equal(t, *expected, actual)
			t.Logf("%d members: %d bytes including Encoder newline", count, len(output))
			// Ordinary human formatting is admitted when its total bytes fit too.
			var indented bytes.Buffer
			require.NoError(t, json.Indent(&indented, output, "", "  "))
			actual, err = ParseProcessGroupRecoveryExpectation(indented.Bytes())
			require.NoError(t, err)
			require.Equal(t, *expected, actual)
		})
	}
}

func TestRecoveryReceiptInclusiveByteBoundary(t *testing.T) {
	group, snapshot, members := recoveryReceiptSizeFixture(160)
	// Include escaped characters so the guard counts encoded bytes, not fields
	// or raw string lengths. Fill the remainder with single-byte ASCII.
	members[0].executable = "worker<&>"
	initial, err := recoveryPreviewExpectation(group, snapshot, members)
	require.NoError(t, err)
	initialBytes := encodeRecoveryReceipt(t, initial)
	members[0].executable += strings.Repeat("x", MaxProcessGroupRecoveryExpectationBytes-len(initialBytes))
	exact, err := recoveryPreviewExpectation(group, snapshot, members)
	require.NoError(t, err)
	output := encodeRecoveryReceipt(t, exact)
	require.Len(t, output, MaxProcessGroupRecoveryExpectationBytes)
	parsed, err := ParseProcessGroupRecoveryExpectation(output)
	require.NoError(t, err)
	require.Equal(t, *exact, parsed)
	compact, err := json.Marshal(exact)
	require.NoError(t, err)
	_, err = ParseProcessGroupRecoveryExpectation(compact)
	require.NoError(t, err)
	_, err = ParseProcessGroupRecoveryExpectation(append(output, ' '))
	require.ErrorContains(t, err, "too large")
	// One extra encoded byte makes even the producer refuse. Removing the
	// newline cannot bypass the shared canonical-output budget at the parser.
	members[0].executable += "x"
	oversize, err := recoveryPreviewExpectation(group, snapshot, members)
	require.ErrorContains(t, err, "too large")
	require.Nil(t, oversize)
	raw, err := json.Marshal(recoveryExpectation(group, snapshot, members))
	require.NoError(t, err)
	require.Len(t, raw, MaxProcessGroupRecoveryExpectationBytes)
	_, err = ParseProcessGroupRecoveryExpectation(raw)
	require.ErrorContains(t, err, "too large")
	_, err = ParseProcessGroupRecoveryExpectation(append(raw, '\n'))
	require.ErrorContains(t, err, "too large")
	require.Equal(t, 16*1024, maxRecordSize, "receipt capacity must not change the registry cap")
}
