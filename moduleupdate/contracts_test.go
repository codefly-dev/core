package moduleupdate_test

import (
	"slices"
	"testing"

	updatev0 "github.com/codefly-dev/core/generated/go/codefly/update/v0"
	"github.com/codefly-dev/core/moduleupdate"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestSnapshotRejectsInvalidEvidence(t *testing.T) {
	tests := map[string]func(*updatev0.ContractSnapshot){
		"schema":        func(s *updatev0.ContractSnapshot) { s.SchemaVersion = 2 },
		"module":        func(s *updatev0.ContractSnapshot) { s.Module = "" },
		"range":         func(s *updatev0.ContractSnapshot) { s.Version = "^1.0.0" },
		"item identity": func(s *updatev0.ContractSnapshot) { s.Items[0].Id = "" },
		"item digest":   func(s *updatev0.ContractSnapshot) { s.Items[0].Digest = "invalid" },
		"duplicate item": func(s *updatev0.ContractSnapshot) {
			s.Items = append(s.Items, proto.Clone(s.Items[0]).(*updatev0.ContractItem))
		},
		"dangling dependency":   func(s *updatev0.ContractSnapshot) { s.Items[0].Dependencies = []string{"absent"} },
		"duplicate dependency":  func(s *updatev0.ContractSnapshot) { s.Items[0].Dependencies = []string{account, account} },
		"unknown nested schema": func(s *updatev0.ContractSnapshot) { s.Items[0].ProtoReflect().SetUnknown([]byte{0x78, 1}) },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			snapshot, _ := fixture(t)
			mutate(snapshot)
			_, err := moduleupdate.PrepareSnapshot(snapshot)
			require.Error(t, err)
		})
	}
	_, err := moduleupdate.PrepareSnapshot(nil)
	require.Error(t, err)
}

func TestReleaseDiffIdentityAndCanonicalChanges(t *testing.T) {
	baseline, pin := fixture(t)
	_, err := moduleupdate.BuildReleaseDiff(baseline, baseline)
	require.Error(t, err)
	other := proto.Clone(baseline).(*updatev0.ContractSnapshot)
	other.Module = "other/accounts"
	other, err = moduleupdate.PrepareSnapshot(other)
	require.NoError(t, err)
	_, err = moduleupdate.BuildReleaseDiff(baseline, other)
	require.Error(t, err)
	diff := release(t, baseline, func(s *updatev0.ContractSnapshot) {
		change(get)(s)
		s.Items = append(s.Items, &updatev0.ContractItem{Id: "new/capability", Digest: changedDigest})
	})
	original, err := moduleupdate.MarshalReleaseDiff(diff)
	require.NoError(t, err)
	slices.Reverse(diff.Changes)
	slices.Reverse(diff.After.Items)
	canonical, err := moduleupdate.MarshalReleaseDiff(diff)
	require.NoError(t, err)
	require.Equal(t, string(original), string(canonical))
	result := moduleupdate.Evaluate(diff, pin)
	require.Equal(t, updatev0.Verdict_VERDICT_BREAKING, result.Verdict)
	require.Len(t, result.Capabilities, 1)
	diff.Changes = append(diff.Changes, diff.Changes[0])
	require.Error(t, moduleupdate.ValidateReleaseDiff(diff))
	_, err = moduleupdate.MarshalReleaseDiff(diff)
	require.Error(t, err)
}

func TestDifferentClientVersionsAndStableResultOrder(t *testing.T) {
	baseline, pin := fixture(t)
	second := proto.Clone(pin.Clients[0]).(*updatev0.ClientPin)
	second.Version = "1.3.0"
	pin.Clients = append(pin.Clients, second)
	diff := release(t, baseline, change(get))
	expected := moduleupdate.Evaluate(diff, pin)
	require.Len(t, expected.Breaking, 2)
	slices.Reverse(pin.Clients)
	actual := moduleupdate.Evaluate(diff, pin)
	require.True(t, proto.Equal(expected, actual))
	for _, item := range actual.Breaking {
		require.Contains(t, item.Reason, "candidate contract differs from the pinned client contract")
	}
}
