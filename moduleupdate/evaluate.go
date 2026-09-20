package moduleupdate

import (
	"fmt"
	"slices"
	"strings"

	updatev0 "github.com/codefly-dev/core/generated/go/codefly/update/v0"
	"github.com/codefly-dev/core/runnable"
	"google.golang.org/protobuf/encoding/protojson"
)

func EvaluateJSON(release, consumer []byte) *updatev0.UpdateResult {
	pin := new(updatev0.ConsumerPin)
	if err := protojson.Unmarshal(consumer, pin); err != nil {
		return undetermined(new(updatev0.UpdateResult), fmt.Errorf("decode consumer pin: %w", err))
	}
	diff := new(updatev0.ReleaseDiff)
	if err := protojson.Unmarshal(release, diff); err != nil {
		return undetermined(resultFor(nil, pin), err)
	}
	return Evaluate(diff, pin)
}

func Evaluate(diff *updatev0.ReleaseDiff, pin *updatev0.ConsumerPin) *updatev0.UpdateResult {
	prepared, err := PrepareReleaseDiff(diff)
	if err != nil {
		return undetermined(resultFor(diff, pin), err)
	}
	return prepared.Evaluate(pin)
}

func (prepared *PreparedRelease) Evaluate(pin *updatev0.ConsumerPin) *updatev0.UpdateResult {
	if prepared == nil || prepared.diff == nil {
		return undetermined(resultFor(nil, pin), fmt.Errorf("prepared release is required"))
	}
	diff := prepared.diff
	result := resultFor(diff, pin)
	if !diff.Before.Complete || !diff.After.Complete {
		return undetermined(result, fmt.Errorf("public contract coverage is incomplete"))
	}
	if _, err := runnable.CanonicalJSON(pin); err != nil {
		return undetermined(result, err)
	}
	if pin.SchemaVersion != 1 || strings.TrimSpace(pin.Consumer) == "" || !pin.UsageComplete {
		return undetermined(result, fmt.Errorf("consumer requires schema version 1, identity, and complete usage"))
	}
	if pin.Module != diff.Before.Module || pin.Version != diff.Before.Version || pin.SnapshotDigest != diff.Before.Digest {
		return undetermined(result, fmt.Errorf("consumer module/version/snapshot digest does not match the release baseline"))
	}
	before, after := prepared.before, prepared.after
	used := make(map[string][]*updatev0.AffectedItem)
	checkUses := func(uses []*updatev0.ContractUse, client, version string, checkClosure bool) {
		declared := make(map[string]bool)
		for _, use := range uses {
			id := use.GetItem()
			affected := &updatev0.AffectedItem{Item: id, Client: client, ClientVersion: version}
			incompatible := false
			old := before[id]
			next := after[id]
			dependencies := slices.Clone(use.GetDependencies())
			slices.Sort(dependencies)
			candidateDependencies := slices.Clone(next.GetDependencies())
			slices.Sort(candidateDependencies)
			switch {
			case use == nil || !digestPattern.MatchString(use.Digest):
				affected.Reason = "could not determine: used contract requires an expected digest"
			case next == nil && old != nil:
				affected.Reason = "used contract was removed"
				incompatible = true
			case next == nil:
				affected.Reason = "could not determine: used contract is absent from the candidate"
			case declared[id]:
				affected.Reason = "could not determine: used contract is duplicated"
			case use.Digest != next.Digest:
				change, supported := prepared.semantic[id]
				if !supported || old == nil || use.Digest != old.Digest || !slices.Equal(dependencies, old.Dependencies) {
					affected.Reason = "could not determine: candidate contract differs from the pinned client contract"
					break
				}
				switch change.level {
				case ChangeMajor:
					affected.Reason, incompatible = change.reason, true
				case ChangeMinor:
					result.Capabilities = append(result.Capabilities, &updatev0.AffectedItem{Item: id, Client: client, ClientVersion: version, Reason: change.reason})
				case ChangePatch:
				default:
					affected.Reason = "could not determine: " + change.reason
				}
			case !slices.Equal(dependencies, candidateDependencies):
				affected.Reason = "could not determine: candidate dependencies differ from the pinned client contract"
			}
			if next != nil {
				affected.Documentation = next.Documentation
			} else if old != nil {
				affected.Documentation = old.Documentation
			}
			if affected.Reason != "" {
				if incompatible {
					result.Breaking = append(result.Breaking, affected)
				} else {
					result.Undetermined = append(result.Undetermined, affected)
				}
			}
			declared[id] = true
			used[id] = append(used[id], affected)
		}
		if !checkClosure {
			return
		}
		for _, use := range uses {
			if item := after[use.GetItem()]; item != nil {
				for _, dependency := range item.Dependencies {
					if !declared[dependency] {
						result.Undetermined = append(result.Undetermined, &updatev0.AffectedItem{
							Item: dependency, Client: client, ClientVersion: version,
							Reason: fmt.Sprintf("could not determine: usage of %s omits dependency", item.Id),
						})
					}
				}
			}
		}
	}
	checkUses(pin.Uses, "", "", true)
	clients := make(map[string]bool)
	for _, client := range pin.Clients {
		key := client.GetName() + "\x00" + client.GetVersion()
		if client == nil || strings.TrimSpace(client.Name) == "" || !exactVersion(client.Version) || clients[key] {
			result.Undetermined = append(result.Undetermined, &updatev0.AffectedItem{
				Client: client.GetName(), ClientVersion: client.GetVersion(),
				Reason: "could not determine: client requires a unique identity and exact version",
			})
			continue
		}
		clients[key] = true
		checkUses(client.Uses, client.Name, client.Version, true)
	}
	// Requirements apply without a client pin, including their dependency closure.
	required := make(map[string]bool)
	var includeRequired func(string)
	includeRequired = func(id string) {
		if required[id] {
			return
		}
		required[id] = true
		if len(used[id]) > 0 {
			return
		}
		for _, dependency := range before[id].Dependencies {
			includeRequired(dependency)
		}
	}
	for _, id := range prepared.requiredRoots {
		includeRequired(id)
	}
	for id := range required {
		if len(used[id]) == 0 {
			item := before[id]
			// The module baseline supplies expectations for implicit universal uses.
			checkUses([]*updatev0.ContractUse{{Item: id, Digest: item.Digest, Dependencies: item.Dependencies}}, "", "", false)
		}
	}
	for _, change := range diff.Changes {
		old, next := before[change.Item], after[change.Item]
		if next != nil && next.RequiredByAll && (old == nil || !old.RequiredByAll) && len(used[change.Item]) == 0 {
			result.Undetermined = append(result.Undetermined, &updatev0.AffectedItem{
				Item: next.Id, Documentation: next.Documentation,
				Reason: "could not determine: release introduces a requirement for every consumer",
			})
			continue
		}
		if change.Kind == updatev0.ChangeKind_CHANGE_KIND_ADDED && len(used[change.Item]) == 0 {
			result.Capabilities = append(result.Capabilities, &updatev0.AffectedItem{
				Item: next.Id, Documentation: next.Documentation, Reason: "new optional contract",
			})
			continue
		}
	}
	result.Verdict = updatev0.Verdict_VERDICT_SAFE
	if len(result.Capabilities) > 0 {
		result.Verdict = updatev0.Verdict_VERDICT_NEW_CAPABILITY
	}
	if len(result.Undetermined) > 0 {
		result.Verdict = updatev0.Verdict_VERDICT_UNDETERMINED
	}
	if len(result.Breaking) > 0 {
		result.Verdict = updatev0.Verdict_VERDICT_BREAKING
	}
	for _, items := range [][]*updatev0.AffectedItem{result.Breaking, result.Capabilities, result.Undetermined} {
		slices.SortFunc(items, func(a, b *updatev0.AffectedItem) int {
			for _, pair := range [][2]string{{a.Item, b.Item}, {a.Client, b.Client}, {a.ClientVersion, b.ClientVersion}, {a.Reason, b.Reason}} {
				if order := strings.Compare(pair[0], pair[1]); order != 0 {
					return order
				}
			}
			return 0
		})
	}
	return result
}

func resultFor(diff *updatev0.ReleaseDiff, pin *updatev0.ConsumerPin) *updatev0.UpdateResult {
	return &updatev0.UpdateResult{
		Consumer: pin.GetConsumer(), Module: pin.GetModule(), FromVersion: pin.GetVersion(),
		ToVersion: diff.GetAfter().GetVersion(),
	}
}

func undetermined(result *updatev0.UpdateResult, err error) *updatev0.UpdateResult {
	result.Verdict = updatev0.Verdict_VERDICT_UNDETERMINED
	result.Undetermined = append(result.Undetermined, &updatev0.AffectedItem{Reason: "could not determine: " + err.Error()})
	return result
}
