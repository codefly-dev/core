package moduleupdate

import (
	"strings"

	"github.com/Masterminds/semver/v3"
	updatev0 "github.com/codefly-dev/core/generated/go/codefly/update/v0"
)

type ChangeLevel string

const (
	ChangePatch        ChangeLevel = "patch"
	ChangeMinor        ChangeLevel = "minor"
	ChangeMajor        ChangeLevel = "major"
	ChangeUndetermined ChangeLevel = "undetermined"
)

type ChangeClassification struct {
	Level        ChangeLevel              `json:"level"`
	BeforeStage  string                   `json:"beforeStage"`
	AfterStage   string                   `json:"afterStage"`
	Undetermined []*updatev0.AffectedItem `json:"undetermined,omitempty"`
}

// ClassifyContractChange describes publisher-wide contract changes, not whether
// the implementation is correct or any particular consumer may adopt it.
func ClassifyContractChange(diff *updatev0.ReleaseDiff) *ChangeClassification {
	result := &ChangeClassification{Level: ChangeUndetermined}
	prepared, err := PrepareReleaseDiff(diff)
	if err != nil {
		result.Undetermined = []*updatev0.AffectedItem{{Reason: err.Error()}}
		return result
	}
	diff = prepared.diff
	result.BeforeStage = releaseStage(diff.Before.Version)
	result.AfterStage = releaseStage(diff.After.Version)
	if !diff.Before.Complete || !diff.After.Complete {
		result.Undetermined = []*updatev0.AffectedItem{{Reason: "public contract coverage is incomplete"}}
		return result
	}
	result.Level = ChangePatch
	for _, change := range diff.Changes {
		switch change.Kind {
		case updatev0.ChangeKind_CHANGE_KIND_REMOVED:
			result.Level = ChangeMajor
		case updatev0.ChangeKind_CHANGE_KIND_ADDED:
			if prepared.after[change.Item].RequiredByAll {
				result.Undetermined = append(result.Undetermined, &updatev0.AffectedItem{Item: change.Item, Reason: "new universal requirement needs semantic qualification"})
			} else if result.Level == ChangePatch {
				result.Level = ChangeMinor
			}
		case updatev0.ChangeKind_CHANGE_KIND_MODIFIED:
			result.Undetermined = append(result.Undetermined, &updatev0.AffectedItem{Item: change.Item, Reason: "contract content or dependencies changed; digest evidence cannot classify semantic compatibility"})
		}
	}
	if len(result.Undetermined) > 0 {
		result.Level = ChangeUndetermined
	}
	return result
}

func releaseStage(value string) string {
	version, err := semver.StrictNewVersion(strings.TrimPrefix(value, "v"))
	if err != nil {
		return "revision"
	}
	if version.Prerelease() != "" {
		if version.Major() == 0 {
			return "development-prerelease"
		}
		return "prerelease"
	}
	if version.Major() == 0 {
		return "development"
	}
	return "stable"
}
