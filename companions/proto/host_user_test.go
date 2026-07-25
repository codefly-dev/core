package proto

import (
	"fmt"
	"os"
	"runtime"
	"testing"
)

// TestHostUserSpec pins the platform contract the proto companions rely on:
// on Linux the companion must run as the invoking host user so bind-mounted
// generated output is host-owned (readable by a non-root CI runner); on other
// platforms it keeps the image default.
func TestHostUserSpec(t *testing.T) {
	spec := hostUserSpec()
	if runtime.GOOS == "linux" {
		want := fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid())
		if spec != want {
			t.Fatalf("expected %q on linux, got %q", want, spec)
		}
		return
	}
	if spec != "" {
		t.Fatalf("expected empty user spec off linux, got %q", spec)
	}
}
