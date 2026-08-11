package resources

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestInspectStorageFilesystemCoalescesRealSameVolumeRoots(t *testing.T) {
	root := t.TempDir()
	first, err := InspectStorageFilesystem(root)
	if err != nil {
		t.Fatalf("InspectStorageFilesystem(existing): %v", err)
	}
	second, err := InspectStorageFilesystem(filepath.Join(root, "future", "cassette", "payloads"))
	if err != nil {
		t.Fatalf("InspectStorageFilesystem(future): %v", err)
	}
	if first.AuthorityID == "" || !strings.HasPrefix(first.AuthorityID, "storage/sha256:") {
		t.Fatalf("authority id = %q", first.AuthorityID)
	}
	if second.AuthorityID != first.AuthorityID {
		t.Fatalf("same-volume authority ids differ: %q != %q", second.AuthorityID, first.AuthorityID)
	}
	if first.TotalBytes == 0 || first.AvailableBytes > first.TotalBytes || second.TotalBytes != first.TotalBytes {
		t.Fatalf("capacity observations are inconsistent: first=%+v second=%+v", first, second)
	}
}

func TestInspectStorageFilesystemRejectsMissingRoot(t *testing.T) {
	if _, err := InspectStorageFilesystem("  "); err == nil {
		t.Fatal("empty root unexpectedly admitted")
	}
}
