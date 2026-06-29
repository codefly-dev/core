package base

import "testing"

// TestShouldReapGroup pins the orphan-reap decision for process groups — the
// regression guard for the "codefly leaves resources hanging" bug, where a
// service binary survived its SIGKILLed parent and kept holding its port.
func TestShouldReapGroup(t *testing.T) {
	cases := []struct {
		name       string
		hasParent  bool
		ownerAlive bool
		groupAlive bool
		want       bool
	}{
		// The bug: owner (CLI) dead, but the service binary's group is still
		// alive holding its port → MUST reap.
		{"orphan still holding port", true, false, true, true},
		// Live owner (concurrent run / live detach) → never reap, it's managed.
		{"live owner, group alive", true, true, true, false},
		{"live owner, group dead", true, true, false, false},
		// Owner dead, group already gone → nothing to kill.
		{"owner dead, group dead", true, false, false, false},
		// Legacy file with no parent recorded → treat as orphan: reap iff alive.
		{"no parent recorded, alive", false, false, true, true},
		{"no parent recorded, dead", false, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldReapGroup(tc.hasParent, tc.ownerAlive, tc.groupAlive); got != tc.want {
				t.Errorf("shouldReapGroup(hasParent=%v, ownerAlive=%v, groupAlive=%v) = %v, want %v",
					tc.hasParent, tc.ownerAlive, tc.groupAlive, got, tc.want)
			}
		})
	}
}
