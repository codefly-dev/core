package moduleupdate_test

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
	"testing"

	updatev0 "github.com/codefly-dev/core/generated/go/codefly/update/v0"
	"github.com/codefly-dev/core/moduleupdate"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

const (
	get           = "/accounts.v1.Accounts/Get"
	list          = "/accounts.v1.Accounts/List"
	account       = "accounts.v1.Account"
	registration  = "registration/publisher-authorization"
	changedDigest = "sha256:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
)

func fixture(t *testing.T) (*updatev0.ContractSnapshot, *updatev0.ConsumerPin) {
	t.Helper()
	data, err := os.ReadFile("testdata/contracts.yaml")
	require.NoError(t, err)
	var document map[string]any
	require.NoError(t, yaml.Unmarshal(data, &document))
	data, err = json.Marshal(document)
	require.NoError(t, err)
	snapshot := new(updatev0.ContractSnapshot)
	require.NoError(t, protojson.Unmarshal(data, snapshot))
	snapshot, err = moduleupdate.PrepareSnapshot(snapshot)
	require.NoError(t, err)
	pin := &updatev0.ConsumerPin{
		SchemaVersion: 1, Consumer: "deployment-a", Module: snapshot.Module,
		Version: snapshot.Version, SnapshotDigest: snapshot.Digest, UsageComplete: true,
		Clients: []*updatev0.ClientPin{{Name: "accounts-sdk-go", Version: "v1.2.0", Uses: uses(snapshot, get, account)}},
	}
	return snapshot, pin
}

func uses(snapshot *updatev0.ContractSnapshot, ids ...string) []*updatev0.ContractUse {
	var result []*updatev0.ContractUse
	for _, id := range ids {
		for _, item := range snapshot.Items {
			if item.Id == id {
				result = append(result, &updatev0.ContractUse{Item: id, Digest: item.Digest, Dependencies: slices.Clone(item.Dependencies)})
			}
		}
	}
	return result
}

func release(t *testing.T, baseline *updatev0.ContractSnapshot, mutate func(*updatev0.ContractSnapshot)) *updatev0.ReleaseDiff {
	t.Helper()
	next := proto.Clone(baseline).(*updatev0.ContractSnapshot)
	next.Version = "1.1.0"
	if mutate != nil {
		mutate(next)
	}
	next, err := moduleupdate.PrepareSnapshot(next)
	require.NoError(t, err)
	diff, err := moduleupdate.BuildReleaseDiff(baseline, next)
	require.NoError(t, err)
	return diff
}

func change(id string) func(*updatev0.ContractSnapshot) {
	return func(snapshot *updatev0.ContractSnapshot) {
		for _, item := range snapshot.Items {
			if item.Id == id {
				item.Digest = changedDigest
			}
		}
	}
}

func TestVerdictDependsOnPinnedConsumer(t *testing.T) {
	baseline, pin := fixture(t)
	diff := release(t, baseline, change(get))
	result := moduleupdate.Evaluate(diff, pin)
	require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, result.Verdict)
	require.Len(t, result.Undetermined, 1)
	require.Equal(t, get, result.Undetermined[0].Item)
	require.Equal(t, "accounts-sdk-go", result.Undetermined[0].Client)
	require.Equal(t, "v1.2.0", result.Undetermined[0].ClientVersion)

	pin.Consumer = "deployment-b"
	pin.Clients[0].Uses = uses(baseline, list, account)
	result = moduleupdate.Evaluate(diff, pin)
	require.Equal(t, updatev0.Verdict_VERDICT_SAFE, result.Verdict)
	require.Empty(t, result.Undetermined)
}

func TestCandidateRestoresPinnedClientContract(t *testing.T) {
	for _, id := range []string{get, account} {
		t.Run(id, func(t *testing.T) {
			sdkSnapshot, pin := fixture(t)
			baseline := proto.Clone(sdkSnapshot).(*updatev0.ContractSnapshot)
			baseline.Version = "1.1.0"
			change(id)(baseline)
			var err error
			baseline, err = moduleupdate.PrepareSnapshot(baseline)
			require.NoError(t, err)
			pin.Version, pin.SnapshotDigest = baseline.Version, baseline.Digest
			candidate := proto.Clone(sdkSnapshot).(*updatev0.ContractSnapshot)
			candidate.Version = "1.2.0"
			candidate, err = moduleupdate.PrepareSnapshot(candidate)
			require.NoError(t, err)
			diff, err := moduleupdate.BuildReleaseDiff(baseline, candidate)
			require.NoError(t, err)
			result := moduleupdate.Evaluate(diff, pin)
			require.Equal(t, updatev0.Verdict_VERDICT_SAFE, result.Verdict, result)
			require.Empty(t, result.Undetermined)
			change(id)(candidate)
			candidate, err = moduleupdate.PrepareSnapshot(candidate)
			require.NoError(t, err)
			diff, err = moduleupdate.BuildReleaseDiff(baseline, candidate)
			require.NoError(t, err)
			require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, moduleupdate.Evaluate(diff, pin).Verdict)
		})
	}
}

func TestCandidateDependenciesMustMatchClientExpectations(t *testing.T) {
	baseline, pin := fixture(t)
	diff := release(t, baseline, func(s *updatev0.ContractSnapshot) {
		for _, item := range s.Items {
			if item.Id == get {
				item.Dependencies = []string{registration}
			}
		}
	})
	result := moduleupdate.Evaluate(diff, pin)
	require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, result.Verdict)
	require.Contains(t, result.Undetermined[0].Reason, "candidate dependencies differ")
	// A client generated for the candidate has independently pinned its new closure.
	pin.Clients[0].Uses = uses(diff.After, get, registration)
	result = moduleupdate.Evaluate(diff, pin)
	require.Equal(t, updatev0.Verdict_VERDICT_SAFE, result.Verdict, result)
}

func TestAlreadyPinnedAddedCapabilityIsNotNewToConsumer(t *testing.T) {
	baseline, pin := fixture(t)
	diff := release(t, baseline, func(s *updatev0.ContractSnapshot) {
		s.Items = append(s.Items, &updatev0.ContractItem{Id: "new/rpc", Digest: changedDigest})
	})
	pin.Clients[0].Uses = uses(diff.After, "new/rpc")
	result := moduleupdate.Evaluate(diff, pin)
	require.Equal(t, updatev0.Verdict_VERDICT_SAFE, result.Verdict, result)
	require.Empty(t, result.Capabilities)
}

func TestExplicitUniversalPinUsesCandidateDependencyClosure(t *testing.T) {
	baseline, pin := fixture(t)
	for _, item := range baseline.Items {
		if item.Id == registration {
			item.Dependencies = []string{list}
		}
	}
	var err error
	baseline, err = moduleupdate.PrepareSnapshot(baseline)
	require.NoError(t, err)
	pin.SnapshotDigest = baseline.Digest
	diff := release(t, baseline, func(s *updatev0.ContractSnapshot) {
		s.Items = slices.DeleteFunc(s.Items, func(item *updatev0.ContractItem) bool { return item.Id == list })
		for _, item := range s.Items {
			if item.Id == registration {
				item.Dependencies = nil
			}
		}
	})
	pin.Uses = uses(diff.After, registration)
	result := moduleupdate.Evaluate(diff, pin)
	require.Equal(t, updatev0.Verdict_VERDICT_SAFE, result.Verdict, result)
}

func TestVerdictChanges(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*updatev0.ContractSnapshot)
		verdict updatev0.Verdict
		item    string
	}{
		{"unchanged", nil, updatev0.Verdict_VERDICT_SAFE, ""},
		{"unrelated RPC", change(list), updatev0.Verdict_VERDICT_SAFE, ""},
		{"used RPC", change(get), updatev0.Verdict_VERDICT_UNDETERMINED, get},
		{"transitive type", change(account), updatev0.Verdict_VERDICT_UNDETERMINED, account},
		{"registration without SDK use", change(registration), updatev0.Verdict_VERDICT_UNDETERMINED, registration},
		{"remove used RPC", func(s *updatev0.ContractSnapshot) {
			s.Items = slices.DeleteFunc(s.Items, func(i *updatev0.ContractItem) bool { return i.Id == get })
		}, updatev0.Verdict_VERDICT_BREAKING, get},
		{"remove unused RPC", func(s *updatev0.ContractSnapshot) {
			s.Items = slices.DeleteFunc(s.Items, func(i *updatev0.ContractItem) bool { return i.Id == list })
		}, updatev0.Verdict_VERDICT_SAFE, ""},
		{"new optional RPC", func(s *updatev0.ContractSnapshot) {
			s.Items = append(s.Items, &updatev0.ContractItem{Id: "/accounts.v1.Accounts/Watch", Digest: changedDigest, Documentation: "https://example.com/watch"})
		}, updatev0.Verdict_VERDICT_NEW_CAPABILITY, "/accounts.v1.Accounts/Watch"},
		{"new universal requirement", func(s *updatev0.ContractSnapshot) {
			s.Items = append(s.Items, &updatev0.ContractItem{Id: "registration/token", Digest: changedDigest, RequiredByAll: true})
		}, updatev0.Verdict_VERDICT_UNDETERMINED, "registration/token"},
		{"optional becomes required", func(s *updatev0.ContractSnapshot) {
			for _, item := range s.Items {
				if item.Id == list {
					item.RequiredByAll = true
				}
			}
		}, updatev0.Verdict_VERDICT_UNDETERMINED, list},
		{"changed dependency edge", func(s *updatev0.ContractSnapshot) {
			for _, item := range s.Items {
				if item.Id == get {
					item.Dependencies = nil
				}
			}
		}, updatev0.Verdict_VERDICT_UNDETERMINED, get},
		{"documentation only", func(s *updatev0.ContractSnapshot) {
			s.Items[0].Documentation = "https://example.com/new-docs"
		}, updatev0.Verdict_VERDICT_SAFE, ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			baseline, pin := fixture(t)
			result := moduleupdate.Evaluate(release(t, baseline, test.mutate), pin)
			require.Equal(t, test.verdict, result.Verdict, result)
			if test.verdict == updatev0.Verdict_VERDICT_BREAKING {
				require.Equal(t, test.item, result.Breaking[0].Item)
			}
			if test.verdict == updatev0.Verdict_VERDICT_UNDETERMINED {
				require.Equal(t, test.item, result.Undetermined[0].Item)
			}
			if test.verdict == updatev0.Verdict_VERDICT_NEW_CAPABILITY {
				require.Equal(t, test.item, result.Capabilities[0].Item)
				require.Equal(t, "https://example.com/watch", result.Capabilities[0].Documentation)
			}
		})
	}
}

func TestUnknownIsUndetermined(t *testing.T) {
	tests := map[string]func(*updatev0.ReleaseDiff, *updatev0.ConsumerPin){
		"missing baseline": func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) { d.Before = nil },
		"incomplete release": func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) {
			d.After.Complete = false
			var err error
			d.After, err = moduleupdate.PrepareSnapshot(d.After)
			require.NoError(t, err)
		},
		"incomplete usage":   func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) { p.UsageComplete = false },
		"missing consumer":   func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) { p.Consumer = "" },
		"other module":       func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) { p.Module = "other/module" },
		"skipped baseline":   func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) { p.Version = "0.9.0" },
		"stale snapshot":     func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) { p.SnapshotDigest = changedDigest },
		"stale client":       func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) { p.Clients[0].Uses[0].Digest = changedDigest },
		"missing type usage": func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) { p.Clients[0].Uses = p.Clients[0].Uses[:1] },
		"unknown item":       func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) { p.Clients[0].Uses[0].Item = "absent" },
		"duplicate use": func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) {
			p.Clients[0].Uses = append(p.Clients[0].Uses, p.Clients[0].Uses[0])
		},
		"client range":        func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) { p.Clients[0].Version = "^1.0.0" },
		"duplicate client":    func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) { p.Clients = append(p.Clients, p.Clients[0]) },
		"unknown pin schema":  func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) { p.SchemaVersion = 2 },
		"unknown diff schema": func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) { d.SchemaVersion = 2 },
		"unknown nested fields": func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) {
			p.Clients[0].ProtoReflect().SetUnknown([]byte{0x78, 1})
		},
		"tampered content": func(d *updatev0.ReleaseDiff, p *updatev0.ConsumerPin) { d.After.Items[0].Digest = changedDigest },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			baseline, pin := fixture(t)
			diff := release(t, baseline, nil)
			mutate(diff, pin)
			result := moduleupdate.Evaluate(diff, pin)
			require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, result.Verdict)
			require.Empty(t, result.Breaking)
			require.Contains(t, result.Undetermined[0].Reason, "could not determine")
		})
	}
	result := moduleupdate.Evaluate(nil, nil)
	require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, result.Verdict)
	baseline, _ := fixture(t)
	require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, moduleupdate.Evaluate(release(t, baseline, nil), nil).Verdict)
}

func TestDemonstratedBreakAndMissingEvidenceStayDistinct(t *testing.T) {
	baseline, pin := fixture(t)
	diff := release(t, baseline, func(s *updatev0.ContractSnapshot) {
		s.Items = slices.DeleteFunc(s.Items, func(item *updatev0.ContractItem) bool { return item.Id == get })
		change(account)(s)
	})
	result := moduleupdate.Evaluate(diff, pin)
	require.Equal(t, updatev0.Verdict_VERDICT_BREAKING, result.Verdict)
	require.Len(t, result.Breaking, 1)
	require.Equal(t, get, result.Breaking[0].Item)
	require.Len(t, result.Undetermined, 1)
	require.Equal(t, account, result.Undetermined[0].Item)
}

func TestDiffCannotOmitChanges(t *testing.T) {
	baseline, pin := fixture(t)
	diff := release(t, baseline, change(get))
	diff.Changes = nil
	require.ErrorContains(t, moduleupdate.ValidateReleaseDiff(diff), "delta")
	require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, moduleupdate.Evaluate(diff, pin).Verdict)
}

func TestOfflineJSONAndDeterminism(t *testing.T) {
	baseline, pin := fixture(t)
	original := proto.Clone(baseline)
	reordered := proto.Clone(baseline).(*updatev0.ContractSnapshot)
	slices.Reverse(reordered.Items)
	normalized, err := moduleupdate.PrepareSnapshot(reordered)
	require.NoError(t, err)
	require.True(t, proto.Equal(baseline, original))
	require.Equal(t, baseline.Digest, normalized.Digest)
	diff := release(t, baseline, change(account))
	diffJSON, err := moduleupdate.MarshalReleaseDiff(diff)
	require.NoError(t, err)
	parsed, err := moduleupdate.ParseReleaseDiff(diffJSON)
	require.NoError(t, err)
	second, err := moduleupdate.MarshalReleaseDiff(parsed)
	require.NoError(t, err)
	require.Equal(t, string(diffJSON), string(second))
	pinJSON, err := protojson.Marshal(pin)
	require.NoError(t, err)
	expected := moduleupdate.Evaluate(diff, pin)
	require.True(t, proto.Equal(expected, moduleupdate.EvaluateJSON(diffJSON, pinJSON)))
	for _, input := range [][]byte{nil, []byte("{"), []byte(`{"schema_version":2}`), []byte(strings.Replace(string(diffJSON), `"schema_version":1`, `"unknown_field":1`, 1))} {
		require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, moduleupdate.EvaluateJSON(input, pinJSON).Verdict)
	}
	require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, moduleupdate.EvaluateJSON(diffJSON, []byte(`{"unknown":true}`)).Verdict)
}

func TestGlobalDependencyClosureAndDirectUsage(t *testing.T) {
	baseline, pin := fixture(t)
	for _, item := range baseline.Items {
		if item.Id == registration {
			item.Dependencies = []string{get}
		}
		if item.Id == account {
			item.Dependencies = []string{get}
		}
	}
	var err error
	baseline, err = moduleupdate.PrepareSnapshot(baseline)
	require.NoError(t, err)
	pin.SnapshotDigest = baseline.Digest
	pin.Clients = nil
	result := moduleupdate.Evaluate(release(t, baseline, change(account)), pin)
	require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, result.Verdict)
	require.Equal(t, account, result.Undetermined[0].Item)
	baseline, pin = fixture(t)
	pin.Uses, pin.Clients = pin.Clients[0].Uses, nil
	result = moduleupdate.Evaluate(release(t, baseline, change(get)), pin)
	require.Equal(t, updatev0.Verdict_VERDICT_UNDETERMINED, result.Verdict)
	require.Empty(t, result.Undetermined[0].Client)
}
