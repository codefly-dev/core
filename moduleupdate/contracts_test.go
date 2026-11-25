package moduleupdate_test

import (
	"fmt"
	"slices"
	"testing"

	updatev1 "github.com/codefly-dev/core/generated/go/codefly/update/v1"
	"github.com/codefly-dev/core/moduleupdate"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestSnapshotRejectsInvalidEvidence(t *testing.T) {
	tests := map[string]func(*updatev1.ContractSnapshot){
		"schema":        func(s *updatev1.ContractSnapshot) { s.SchemaVersion = 2 },
		"module":        func(s *updatev1.ContractSnapshot) { s.Module = "" },
		"range":         func(s *updatev1.ContractSnapshot) { s.Version = "^1.0.0" },
		"item identity": func(s *updatev1.ContractSnapshot) { s.Items[0].Id = "" },
		"item digest":   func(s *updatev1.ContractSnapshot) { s.Items[0].Digest = "invalid" },
		"duplicate item": func(s *updatev1.ContractSnapshot) {
			s.Items = append(s.Items, proto.Clone(s.Items[0]).(*updatev1.ContractItem))
		},
		"dangling dependency":   func(s *updatev1.ContractSnapshot) { s.Items[0].Dependencies = []string{"absent"} },
		"duplicate dependency":  func(s *updatev1.ContractSnapshot) { s.Items[0].Dependencies = []string{account, account} },
		"unknown nested schema": func(s *updatev1.ContractSnapshot) { s.Items[0].ProtoReflect().SetUnknown([]byte{0x78, 1}) },
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

func TestPreparedReleaseOwnsItsEvidence(t *testing.T) {
	baseline, pin := fixture(t)
	diff := release(t, baseline, change(get))
	prepared, err := moduleupdate.PrepareReleaseDiff(diff)
	require.NoError(t, err)
	expected := prepared.Evaluate(pin)
	diff.After.Items = nil
	diff.Before.Items = nil
	diff.Changes = nil
	results := make(chan *updatev1.UpdateResult, 32)
	for range cap(results) {
		go func() { results <- prepared.Evaluate(pin) }()
	}
	for range cap(results) {
		result := <-results
		require.True(t, proto.Equal(expected, result))
		result.Undetermined[0].Reason = "mutated result"
	}
	require.True(t, proto.Equal(expected, prepared.Evaluate(pin)))
	require.Equal(t, updatev1.Verdict_VERDICT_UNDETERMINED, new(moduleupdate.PreparedRelease).Evaluate(pin).Verdict)
}

func benchmarkRelease(b testing.TB, size int) (*updatev1.ReleaseDiff, *updatev1.ConsumerPin) {
	b.Helper()
	before := &updatev1.ContractSnapshot{SchemaVersion: 1, Module: "example/accounts", Version: "1.0.0", Complete: true}
	for i := range size {
		before.Items = append(before.Items, &updatev1.ContractItem{Id: fmt.Sprintf("item/%d", i), Digest: changedDigest})
	}
	before, err := moduleupdate.PrepareSnapshot(before)
	require.NoError(b, err)
	after := proto.Clone(before).(*updatev1.ContractSnapshot)
	after.Version = "1.1.0"
	after, err = moduleupdate.PrepareSnapshot(after)
	require.NoError(b, err)
	diff, err := moduleupdate.BuildReleaseDiff(before, after)
	require.NoError(b, err)
	return diff, &updatev1.ConsumerPin{SchemaVersion: 1, Consumer: "deployment", Module: before.Module, Version: before.Version, SnapshotDigest: before.Digest, UsageComplete: true}
}

func TestPreparedEvaluationAllocationsDoNotGrowWithUnusedSurface(t *testing.T) {
	allocations := func(size int) float64 {
		diff, pin := benchmarkRelease(t, size)
		prepared, err := moduleupdate.PrepareReleaseDiff(diff)
		require.NoError(t, err)
		// Race builds randomly discard pooled JSON buffers; average enough runs
		// to measure surface-dependent allocations rather than pool retention.
		return testing.AllocsPerRun(1000, func() { prepared.Evaluate(pin) })
	}
	require.LessOrEqual(t, allocations(2000), allocations(10)+2)
}

func BenchmarkReleaseEvaluation(b *testing.B) {
	diff, pin := benchmarkRelease(b, 2000)
	prepared, err := moduleupdate.PrepareReleaseDiff(diff)
	require.NoError(b, err)
	b.Run("unprepared", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			moduleupdate.Evaluate(diff, pin)
		}
	})
	b.Run("prepared", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			prepared.Evaluate(pin)
		}
	})
}

func TestReleaseDiffIdentityAndCanonicalChanges(t *testing.T) {
	baseline, pin := fixture(t)
	_, err := moduleupdate.BuildReleaseDiff(baseline, baseline)
	require.Error(t, err)
	other := proto.Clone(baseline).(*updatev1.ContractSnapshot)
	other.Module = "other/accounts"
	other, err = moduleupdate.PrepareSnapshot(other)
	require.NoError(t, err)
	_, err = moduleupdate.BuildReleaseDiff(baseline, other)
	require.Error(t, err)
	diff := release(t, baseline, func(s *updatev1.ContractSnapshot) {
		change(get)(s)
		s.Items = append(s.Items, &updatev1.ContractItem{Id: "new/capability", Digest: changedDigest})
	})
	original, err := moduleupdate.MarshalReleaseDiff(diff)
	require.NoError(t, err)
	slices.Reverse(diff.Changes)
	slices.Reverse(diff.After.Items)
	canonical, err := moduleupdate.MarshalReleaseDiff(diff)
	require.NoError(t, err)
	require.Equal(t, string(original), string(canonical))
	result := moduleupdate.Evaluate(diff, pin)
	require.Equal(t, updatev1.Verdict_VERDICT_UNDETERMINED, result.Verdict)
	require.Len(t, result.Capabilities, 1)
	diff.Changes = append(diff.Changes, diff.Changes[0])
	require.Error(t, moduleupdate.ValidateReleaseDiff(diff))
	_, err = moduleupdate.MarshalReleaseDiff(diff)
	require.Error(t, err)
}

func TestDifferentClientVersionsAndStableResultOrder(t *testing.T) {
	baseline, pin := fixture(t)
	second := proto.Clone(pin.Clients[0]).(*updatev1.ClientPin)
	second.Version = "1.3.0"
	pin.Clients = append(pin.Clients, second)
	diff := release(t, baseline, change(get))
	expected := moduleupdate.Evaluate(diff, pin)
	require.Len(t, expected.Undetermined, 2)
	slices.Reverse(pin.Clients)
	actual := moduleupdate.Evaluate(diff, pin)
	require.True(t, proto.Equal(expected, actual))
	for _, item := range actual.Undetermined {
		require.Contains(t, item.Reason, "candidate contract differs from the pinned client contract")
	}
}
