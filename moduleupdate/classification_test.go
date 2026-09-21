package moduleupdate_test

import (
	"slices"
	"testing"

	updatev0 "github.com/codefly-dev/core/generated/go/codefly/update/v0"
	"github.com/codefly-dev/core/moduleupdate"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
)

func TestPublisherClassificationIsNotConsumerCompatibility(t *testing.T) {
	baseline, pin := fixture(t)
	for _, tc := range []struct {
		name   string
		mutate func(*updatev0.ContractSnapshot)
		level  moduleupdate.ChangeLevel
	}{
		{"unchanged", nil, moduleupdate.ChangePatch},
		{"optional addition", func(s *updatev0.ContractSnapshot) {
			s.Items = append(s.Items, &updatev0.ContractItem{Id: "optional", Digest: changedDigest})
		}, moduleupdate.ChangeMinor},
		{"unused RPC removed", func(s *updatev0.ContractSnapshot) {
			s.Items = slices.DeleteFunc(s.Items, func(i *updatev0.ContractItem) bool { return i.Id == list })
		}, moduleupdate.ChangeMajor},
		{"non-schema requirement removed", func(s *updatev0.ContractSnapshot) {
			s.Items = slices.DeleteFunc(s.Items, func(i *updatev0.ContractItem) bool { return i.Id == registration })
		}, moduleupdate.ChangeMajor},
		{"changed bytes", change(get), moduleupdate.ChangeUndetermined},
		{"new requirement", func(s *updatev0.ContractSnapshot) {
			s.Items = append(s.Items, &updatev0.ContractItem{Id: "auth/new", Digest: changedDigest, RequiredByAll: true})
		}, moduleupdate.ChangeUndetermined},
	} {
		t.Run(tc.name, func(t *testing.T) {
			diff := release(t, baseline, tc.mutate)
			result := moduleupdate.ClassifyContractChange(diff)
			require.Equal(t, tc.level, result.Level)
			if tc.level == moduleupdate.ChangeUndetermined {
				require.NotEmpty(t, result.Undetermined)
			}
			if tc.name == "unused RPC removed" {
				require.Equal(t, updatev0.Verdict_VERDICT_SAFE, moduleupdate.Evaluate(diff, pin).Verdict)
			}
		})
	}
}

func TestReleaseStageNeverInfersSafety(t *testing.T) {
	for _, tc := range []struct{ before, after, stage string }{
		{"1.0.0", "1.0.1", "stable"},
		{"0.1.0", "0.2.0", "development"},
		{"1.0.0-rc.1", "1.0.0-rc.2", "prerelease"},
		{"0.1.0-beta.1", "0.1.0-beta.2", "development-prerelease"},
	} {
		t.Run(tc.stage, func(t *testing.T) {
			before, _ := fixture(t)
			before.Version = tc.before
			before, err := moduleupdate.PrepareSnapshot(before)
			require.NoError(t, err)
			after := proto.Clone(before).(*updatev0.ContractSnapshot)
			after.Version = tc.after
			change(get)(after)
			after, err = moduleupdate.PrepareSnapshot(after)
			require.NoError(t, err)
			diff, err := moduleupdate.BuildReleaseDiff(before, after)
			require.NoError(t, err)
			result := moduleupdate.ClassifyContractChange(diff)
			require.Equal(t, tc.stage, result.BeforeStage)
			require.Equal(t, tc.stage, result.AfterStage)
			require.Equal(t, moduleupdate.ChangeUndetermined, result.Level)
		})
	}
	require.Equal(t, moduleupdate.ChangeUndetermined, moduleupdate.ClassifyContractChange(nil).Level)
}
