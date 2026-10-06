package module

import (
	"slices"
	"testing"
)

// TestTheClosedVocabulariesAreExactlyThese guards the sets WHOLE, which is
// what a fixture cannot do for an UNNAMED added value — the executed round's `extra-operation-allowed` and
// `extra-destination-allowed` mutations widen the set, and every refusal
// fixture keeps refusing because it names a value that is still absent. So the
// sets are asserted whole. Adding an operation or a destination kind without
// changing this test is how a widened vocabulary reaches a reader.
func TestTheClosedVocabulariesAreExactlyThese(t *testing.T) {
	for name, set := range map[string]struct {
		have, want []string
	}{
		"operations": {operations, []string{OperationInvoke, OperationLookup, OperationHeadless}},
		"destination kinds": {destinationKinds, []string{
			DestinationModule, DestinationHost, DestinationPlatformInternal,
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if len(set.have) != len(set.want) {
				t.Fatalf("the %s vocabulary is %v; this contract's is %v — a widened set admits a request no envelope bounds", name, set.have, set.want)
			}
			for _, value := range set.want {
				if !slices.Contains(set.have, value) {
					t.Errorf("the %s vocabulary no longer carries %q", name, value)
				}
			}
		})
	}
	// And the spellings themselves, so renaming one is a visible change.
	for value, want := range map[string]string{
		OperationInvoke: "invoke", OperationLookup: "lookup", OperationHeadless: "headless",
		DestinationModule: "module", DestinationHost: "host", DestinationPlatformInternal: "platform-internal",
	} {
		if value != want {
			t.Errorf("a vocabulary constant is %q where the wire says %q", value, want)
		}
	}
}
