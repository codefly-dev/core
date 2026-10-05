package cell

import (
	"slices"
	"testing"
)

// TestTheClosedVocabulariesAreExactlyThese guards the sets WHOLE, which is
// what a fixture cannot do for an UNNAMED added value — the executed round's `pod-kind-allowed` mutation
// widens the workload kinds, and `unknown-workload-kind` keeps refusing
// because it names a kind that is still absent. So the sets are asserted whole HERE, and the widenings the
// review named ship as fixtures in the kit — this test is a package test and
// is NOT run by a consumer's conformance call, so it protects this reader and
// not a consumer's, and widening one without changing this test is how a reader comes to
// admit a workload the platform's closed approved set would refuse.
func TestTheClosedVocabulariesAreExactlyThese(t *testing.T) {
	kinds := []string{KindDeployment, KindStatefulSet, KindDaemonSet, KindJob, KindCronJob}
	if len(workloadKinds) != len(kinds) {
		t.Fatalf("the workload kinds are %v; this cell's are %v — a widened set admits a pod the approved set does not cover", workloadKinds, kinds)
	}
	for _, kind := range kinds {
		if !slices.Contains(workloadKinds, kind) {
			t.Errorf("the workload kinds no longer carry %q", kind)
		}
	}
	for value, want := range map[string]string{
		KindDeployment: "Deployment", KindStatefulSet: "StatefulSet", KindDaemonSet: "DaemonSet",
		KindJob: "Job", KindCronJob: "CronJob",
	} {
		if value != want {
			t.Errorf("a kind constant is %q where the wire says %q", value, want)
		}
	}
	// The delivery Job's kind is one of them, and the one it must be.
	if KindJob != "Job" {
		t.Error("the delivery kind rule compares against a spelling that is no longer Job")
	}
}
