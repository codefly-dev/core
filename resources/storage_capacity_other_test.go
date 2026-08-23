//go:build !unix

package resources

import (
	"context"
	"testing"
)

// On platforms without statfs the observer must still link and fail cleanly at
// run time rather than break the cross-compiled release build.
func TestInspectStorageFilesystemUnsupportedOffUnix(t *testing.T) {
	if _, err := InspectStorageFilesystem(t.TempDir()); err == nil {
		t.Fatal("expected an unsupported-platform error off unix")
	}
	if _, err := InspectStorageTree(context.Background(), t.TempDir()); err == nil {
		t.Fatal("InspectStorageTree must surface the unsupported-platform error")
	}
}
