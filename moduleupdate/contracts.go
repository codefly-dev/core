// Package moduleupdate computes offline module compatibility from published
// contract evidence and a consumer's exact pins.
package moduleupdate

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/Masterminds/semver/v3"
	updatev0 "github.com/codefly-dev/core/generated/go/codefly/update/v0"
	"github.com/codefly-dev/core/runnable"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const ReleaseDiffFileName = "contracts/update.codefly.json"

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
var revisionPattern = regexp.MustCompile(`^[0-9a-f]{40}([0-9a-f]{24})?$`)

// PreparedRelease owns immutable, validated evidence reusable across consumers.
type PreparedRelease struct {
	diff          *updatev0.ReleaseDiff
	before        map[string]*updatev0.ContractItem
	after         map[string]*updatev0.ContractItem
	requiredRoots []string
	semantic      map[string]semanticChange
}

func PrepareReleaseDiff(diff *updatev0.ReleaseDiff) (*PreparedRelease, error) {
	normalized, err := normalizeReleaseDiff(diff)
	if err != nil {
		return nil, err
	}
	prepared := &PreparedRelease{diff: normalized, before: indexItems(normalized.Before), after: indexItems(normalized.After)}
	for _, item := range normalized.Before.Items {
		if item.RequiredByAll {
			prepared.requiredRoots = append(prepared.requiredRoots, item.Id)
		}
	}
	return prepared, nil
}

// PrepareSnapshot normalizes a copy and binds it to a digest. Producers must
// derive item digests and dependencies from the public contract source of truth.
func PrepareSnapshot(snapshot *updatev0.ContractSnapshot) (*updatev0.ContractSnapshot, error) {
	if snapshot == nil {
		return nil, fmt.Errorf("contract snapshot is required")
	}
	if _, err := runnable.CanonicalJSON(snapshot); err != nil {
		return nil, err
	}
	if snapshot.SchemaVersion != 1 || strings.TrimSpace(snapshot.Module) == "" || !exactVersion(snapshot.Version) {
		return nil, fmt.Errorf("snapshot requires schema version 1, module identity, and exact version")
	}
	normalized := proto.Clone(snapshot).(*updatev0.ContractSnapshot)
	items := make(map[string]*updatev0.ContractItem)
	for _, item := range normalized.Items {
		if item == nil || strings.TrimSpace(item.Id) == "" || !digestPattern.MatchString(item.Digest) {
			return nil, fmt.Errorf("contract item %q requires an identity and SHA-256 digest", item.GetId())
		}
		if items[item.Id] != nil {
			return nil, fmt.Errorf("contract item %q is duplicated", item.Id)
		}
		items[item.Id] = item
		slices.Sort(item.Dependencies)
		for i, dependency := range item.Dependencies {
			if i > 0 && dependency == item.Dependencies[i-1] {
				return nil, fmt.Errorf("contract %q repeats dependency %q", item.Id, dependency)
			}
		}
	}
	for _, item := range normalized.Items {
		for _, dependency := range item.Dependencies {
			if items[dependency] == nil {
				return nil, fmt.Errorf("contract %q references absent dependency %q", item.Id, dependency)
			}
		}
	}
	slices.SortFunc(normalized.Items, func(a, b *updatev0.ContractItem) int { return strings.Compare(a.Id, b.Id) })
	normalized.Digest = ""
	data, err := runnable.CanonicalJSON(normalized)
	if err != nil {
		return nil, err
	}
	normalized.Digest = fmt.Sprintf("sha256:%x", sha256.Sum256(append([]byte("codefly.update.snapshot/v1\x00"), data...)))
	return normalized, nil
}

func exactVersion(version string) bool {
	_, err := semver.StrictNewVersion(strings.TrimPrefix(version, "v"))
	return err == nil || revisionPattern.MatchString(version)
}

func validateSnapshot(snapshot *updatev0.ContractSnapshot) (*updatev0.ContractSnapshot, error) {
	normalized, err := PrepareSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	if snapshot.Digest != normalized.Digest {
		return nil, fmt.Errorf("snapshot %s@%s digest does not match its contracts", snapshot.Module, snapshot.Version)
	}
	return normalized, nil
}

func BuildReleaseDiff(before, after *updatev0.ContractSnapshot) (*updatev0.ReleaseDiff, error) {
	baseline, err := validateSnapshot(before)
	if err != nil {
		return nil, fmt.Errorf("baseline: %w", err)
	}
	candidate, err := validateSnapshot(after)
	if err != nil {
		return nil, fmt.Errorf("candidate: %w", err)
	}
	if baseline.Module != candidate.Module || baseline.Version == candidate.Version {
		return nil, fmt.Errorf("release diff requires the same module at two distinct exact versions")
	}
	diff := &updatev0.ReleaseDiff{SchemaVersion: 1, Before: baseline, After: candidate}
	oldItems, newItems := indexItems(baseline), indexItems(candidate)
	for _, item := range baseline.Items {
		next := newItems[item.Id]
		kind := updatev0.ChangeKind_CHANGE_KIND_UNSPECIFIED
		switch {
		case next == nil:
			kind = updatev0.ChangeKind_CHANGE_KIND_REMOVED
		case item.Digest != next.Digest || item.RequiredByAll != next.RequiredByAll || !slices.Equal(item.Dependencies, next.Dependencies):
			kind = updatev0.ChangeKind_CHANGE_KIND_MODIFIED
		}
		if kind != updatev0.ChangeKind_CHANGE_KIND_UNSPECIFIED {
			diff.Changes = append(diff.Changes, &updatev0.ContractChange{Item: item.Id, Kind: kind})
		}
	}
	for _, item := range candidate.Items {
		if oldItems[item.Id] == nil {
			diff.Changes = append(diff.Changes, &updatev0.ContractChange{Item: item.Id, Kind: updatev0.ChangeKind_CHANGE_KIND_ADDED})
		}
	}
	slices.SortFunc(diff.Changes, func(a, b *updatev0.ContractChange) int { return strings.Compare(a.Item, b.Item) })
	return diff, nil
}

func ValidateReleaseDiff(diff *updatev0.ReleaseDiff) error {
	_, err := normalizeReleaseDiff(diff)
	return err
}

func normalizeReleaseDiff(diff *updatev0.ReleaseDiff) (*updatev0.ReleaseDiff, error) {
	if diff == nil || diff.SchemaVersion != 1 {
		return nil, fmt.Errorf("release diff requires schema version 1")
	}
	if _, err := runnable.CanonicalJSON(diff); err != nil {
		return nil, err
	}
	expected, err := BuildReleaseDiff(diff.Before, diff.After)
	if err != nil {
		return nil, err
	}
	actual := slices.Clone(diff.Changes)
	slices.SortFunc(actual, func(a, b *updatev0.ContractChange) int { return strings.Compare(a.GetItem(), b.GetItem()) })
	if !slices.EqualFunc(actual, expected.Changes, func(a, b *updatev0.ContractChange) bool { return proto.Equal(a, b) }) {
		return nil, fmt.Errorf("release changes do not match the complete snapshot delta")
	}
	return expected, nil
}

func MarshalReleaseDiff(diff *updatev0.ReleaseDiff) ([]byte, error) {
	normalized, err := normalizeReleaseDiff(diff)
	if err != nil {
		return nil, err
	}
	return runnable.CanonicalJSON(normalized)
}

func ParseReleaseDiff(data []byte) (*updatev0.ReleaseDiff, error) {
	diff := new(updatev0.ReleaseDiff)
	if err := protojson.Unmarshal(data, diff); err != nil {
		return nil, fmt.Errorf("decode release diff: %w", err)
	}
	return normalizeReleaseDiff(diff)
}

func indexItems(snapshot *updatev0.ContractSnapshot) map[string]*updatev0.ContractItem {
	items := make(map[string]*updatev0.ContractItem, len(snapshot.Items))
	for _, item := range snapshot.Items {
		items[item.Id] = item
	}
	return items
}
