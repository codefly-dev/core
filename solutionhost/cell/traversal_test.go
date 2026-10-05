package cell

import (
	"strings"
	"testing"
)

// TestEveryTraversalReachesEverySource is the other half of the kit's
// completeness, and the answer to the escapes round three found: a rule can be
// whole and still be applied to only some of what it governs. Deleting
// init-container traversal from containers(), or delivery traversal from
// selectors() or identities(), left every negative fixture refusing exactly as
// before — the rule was intact, it simply no longer looked there.
//
// So each traversal is asserted to reach every source it governs, over the
// valid cell, which carries one of each. A deleted branch fails here, and the
// per-source fixtures beside it fail the kit.
func TestEveryTraversalReachesEverySource(t *testing.T) {
	file, err := Parse(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Run("images reach containers, init containers and the delivery", func(t *testing.T) {
		var container, initContainer, delivery bool
		for _, entry := range file.images() {
			switch {
			case strings.HasSuffix(entry.label, "delivery"):
				delivery = true
			case entry.what == whatInitContainer:
				initContainer = true
			case entry.what == whatContainer:
				container = true
			}
		}
		for _, source := range []struct {
			reached bool
			what    string
		}{{container, "a container"}, {initContainer, "an init container"}, {delivery, "the delivery container"}} {
			if !source.reached {
				t.Errorf("images() does not reach %s, so every image rule skips it", source.what)
			}
		}
	})
	t.Run("selectors reach workloads and the delivery", func(t *testing.T) {
		assertWorkloadAndDelivery(t, "selectors()", labels(file.selectors(), func(entry labelledSelector) string { return entry.label }))
	})
	t.Run("identities reach workloads and the delivery", func(t *testing.T) {
		assertWorkloadAndDelivery(t, "identities()", labels(file.identities(), func(entry labelledIdentity) string { return entry.label }))
	})
}

func labels[T any](entries []T, of func(T) string) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		out = append(out, of(entry))
	}
	return out
}

func assertWorkloadAndDelivery(t *testing.T, traversal string, labels []string) {
	t.Helper()
	var workload, delivery bool
	for _, label := range labels {
		if strings.HasSuffix(label, "delivery") {
			delivery = true
		} else if strings.Contains(label, "workload") {
			workload = true
		}
	}
	if !workload {
		t.Errorf("%s does not reach a workload, so every rule over it skips one", traversal)
	}
	if !delivery {
		t.Errorf("%s does not reach the delivery, so every rule over it skips it", traversal)
	}
}
