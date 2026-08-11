package resources

import (
	"context"
	"os"
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
	if first.TotalBytes == 0 || first.AvailableBytes > first.TotalBytes || second.TotalBytes != first.TotalBytes || first.AllocationUnitBytes == 0 {
		t.Fatalf("capacity observations are inconsistent: first=%+v second=%+v", first, second)
	}
}

func TestInspectStorageTreeConservativelyMeasuresRealFilesystemEntries(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "small.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("small.txt", filepath.Join(root, "nested", "link")); err != nil {
		t.Fatal(err)
	}
	filesystem, err := InspectStorageFilesystem(root)
	if err != nil {
		t.Fatal(err)
	}
	tree, err := InspectStorageTree(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if tree.EntryCount != 4 {
		t.Fatalf("entry count = %d, want root, directory, file, and symlink", tree.EntryCount)
	}
	minimum := uint64(4) * filesystem.AllocationUnitBytes
	if tree.RequiredBytes < minimum || tree.RequiredBytes%filesystem.AllocationUnitBytes != 0 {
		t.Fatalf("required bytes = %d, want at least %d aligned to %d", tree.RequiredBytes, minimum, filesystem.AllocationUnitBytes)
	}
}

func TestInspectStorageFilesystemRejectsMissingRoot(t *testing.T) {
	if _, err := InspectStorageFilesystem("  "); err == nil {
		t.Fatal("empty root unexpectedly admitted")
	}
}
