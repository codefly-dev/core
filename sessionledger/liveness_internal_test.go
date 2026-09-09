package sessionledger

import (
	"fmt"
	"os"
	"testing"

	"github.com/codefly-dev/core/runners/base"
)

// The liveness decision is what recovery uses to conclude an invocation died,
// and "died" is the branch that stops containers and terminates process groups.
// Only a proven absence may reach it: an inspection that failed for any other
// reason leaves the answer unknown, and guessing "dead" from unknown is how a
// live run's dependencies get reaped out from under it.
func TestLivenessTreatsOnlyAProvenAbsenceAsDead(t *testing.T) {
	recorded := Witness{PID: 4242, BootID: "boot", StartID: 99, Executable: "codefly"}
	same := base.ProcessIdentity{PID: 4242, BootID: "boot", StartID: 99, Executable: "codefly"}
	recycled := base.ProcessIdentity{PID: 4242, BootID: "boot", StartID: 100, Executable: "codefly"}

	cases := []struct {
		name     string
		identity base.ProcessIdentity
		err      error
		want     bool
	}{
		{"same process", same, nil, true},
		{"pid reused by a later process", recycled, nil, false},
		{"proven absence", base.ProcessIdentity{}, base.ErrProcessNotFound, false},
		{"wrapped proven absence", base.ProcessIdentity{},
			fmt.Errorf("inspect: %w", base.ErrProcessNotFound), false},
		{"not permitted to inspect", base.ProcessIdentity{}, os.ErrPermission, true},
		{"boot identity unreadable", base.ProcessIdentity{},
			fmt.Errorf("read boot identity: %w", os.ErrInvalid), true},
	}
	for _, tc := range cases {
		if got := liveFromInspection(recorded, tc.identity, tc.err); got != tc.want {
			t.Errorf("%s: liveFromInspection = %v, want %v", tc.name, got, tc.want)
		}
	}
}
