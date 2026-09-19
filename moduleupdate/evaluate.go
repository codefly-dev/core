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
	diff, err := ParseReleaseDiff(release)
	if err != nil {
		return undetermined(resultFor(nil, pin), err)
	}
	return Evaluate(diff, pin)
}

func Evaluate(diff *updatev0.ReleaseDiff, pin *updatev0.ConsumerPin) *updatev0.UpdateResult {
	result := resultFor(diff, pin)
	if err := ValidateReleaseDiff(diff); err != nil {
		return undetermined(result, err)
	}
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
	before, after := indexItems(diff.Before), indexItems(diff.After)
	used := make(map[string][]*updatev0.AffectedItem)
	checkUses := func(uses []*updatev0.ContractUse, client, version string) {
		declared := make(map[string]bool)
		for _, use := range uses {
			id := use.GetItem()
			affected := &updatev0.AffectedItem{Item: id, Client: client, ClientVersion: version}
			old := before[id]
			switch {
			case use == nil || old == nil:
				affected.Reason = "could not determine: used contract is absent from the baseline"
			case declared[id]:
				affected.Reason = "could not determine: used contract is duplicated"
			case use.Digest != old.Digest:
				affected.Reason = "could not determine: pinned contract digest differs from the baseline"
			}
			if affected.Reason != "" {
				result.Breaking = append(result.Breaking, affected)
			}
			declared[id] = true
			used[id] = append(used[id], affected)
		}
		for _, use := range uses {
			if item := before[use.GetItem()]; item != nil {
				for _, dependency := range item.Dependencies {
					if !declared[dependency] {
						result.Breaking = append(result.Breaking, &updatev0.AffectedItem{
							Item: dependency, Client: client, ClientVersion: version,
							Reason: fmt.Sprintf("could not determine: usage of %s omits dependency", item.Id),
						})
					}
				}
			}
		}
	}
	checkUses(pin.Uses, "", "")
	clients := make(map[string]bool)
	for _, client := range pin.Clients {
		key := client.GetName() + "\x00" + client.GetVersion()
		if client == nil || strings.TrimSpace(client.Name) == "" || !exactVersion(client.Version) || clients[key] {
			result.Breaking = append(result.Breaking, &updatev0.AffectedItem{
				Client: client.GetName(), ClientVersion: client.GetVersion(),
				Reason: "could not determine: client requires a unique identity and exact version",
			})
			continue
		}
		clients[key] = true
		checkUses(client.Uses, client.Name, client.Version)
	}
	// Requirements apply without a client pin, including their dependency closure.
	required := make(map[string]bool)
	var includeRequired func(string)
	includeRequired = func(id string) {
		if required[id] {
			return
		}
		required[id] = true
		if len(used[id]) == 0 {
			used[id] = []*updatev0.AffectedItem{{Item: id}}
		}
		for _, dependency := range before[id].Dependencies {
			includeRequired(dependency)
		}
	}
	for _, item := range diff.Before.Items {
		if item.RequiredByAll {
			includeRequired(item.Id)
		}
	}
	for _, change := range diff.Changes {
		old, next := before[change.Item], after[change.Item]
		if next != nil && next.RequiredByAll && (old == nil || !old.RequiredByAll) {
			result.Breaking = append(result.Breaking, &updatev0.AffectedItem{
				Item: next.Id, Documentation: next.Documentation,
				Reason: "could not determine: release introduces a requirement for every consumer",
			})
			continue
		}
		if change.Kind == updatev0.ChangeKind_CHANGE_KIND_ADDED {
			result.Capabilities = append(result.Capabilities, &updatev0.AffectedItem{
				Item: next.Id, Documentation: next.Documentation, Reason: "new optional contract",
			})
			continue
		}
		for _, use := range used[change.Item] {
			documentation := old.Documentation
			if next != nil {
				documentation = next.Documentation
			}
			reason := "used contract changed"
			if change.Kind == updatev0.ChangeKind_CHANGE_KIND_REMOVED {
				reason = "used contract was removed"
			}
			result.Breaking = append(result.Breaking, &updatev0.AffectedItem{
				Item: change.Item, Client: use.Client, ClientVersion: use.ClientVersion,
				Reason: reason, Documentation: documentation,
			})
		}
	}
	result.Verdict = updatev0.Verdict_VERDICT_SAFE
	if len(result.Capabilities) > 0 {
		result.Verdict = updatev0.Verdict_VERDICT_NEW_CAPABILITY
	}
	if len(result.Breaking) > 0 {
		result.Verdict = updatev0.Verdict_VERDICT_BREAKING
	}
	for _, items := range [][]*updatev0.AffectedItem{result.Breaking, result.Capabilities} {
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
	result.Verdict = updatev0.Verdict_VERDICT_BREAKING
	result.Breaking = append(result.Breaking, &updatev0.AffectedItem{Reason: "could not determine: " + err.Error()})
	return result
}
