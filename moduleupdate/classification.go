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
	prepared, err := PrepareReleaseDiff(diff)
	return classifyPrepared(prepared, err)
}

func ClassifyContractChangeWithSources(diff *updatev0.ReleaseDiff, before, after map[string]ContractSource) *ChangeClassification {
	prepared, err := PrepareReleaseDiffWithSources(diff, before, after)
	return classifyPrepared(prepared, err)
}

func classifyPrepared(prepared *PreparedRelease, err error) *ChangeClassification {
	result := &ChangeClassification{Level: ChangeUndetermined}
	if err != nil {
		result.Undetermined = []*updatev0.AffectedItem{{Reason: err.Error()}}
		return result
	}
	diff := prepared.diff
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
			semantic, exists := prepared.semantic[change.Item]
			if !exists || semantic.level == ChangeUndetermined || (!prepared.before[change.Item].RequiredByAll && prepared.after[change.Item].RequiredByAll) {
				result.Undetermined = append(result.Undetermined, &updatev0.AffectedItem{Item: change.Item, Reason: "contract content, dependencies or requirements need semantic qualification"})
			} else if semantic.level == ChangeMajor {
				result.Level = ChangeMajor
			} else if semantic.level == ChangeMinor && result.Level == ChangePatch {
				result.Level = ChangeMinor
			}
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
