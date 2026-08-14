package base

import (
	"os"
	"path/filepath"
	"testing"
)

// TestProcessGroupRegistryIsolatesRecordContracts proves independently
// released agents cannot make the current reaper parse, quarantine, or signal
// from a foreign record contract that shares the user's Codefly home.
func TestProcessGroupRegistryIsolatesRecordContracts(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := filepath.Join(home, ".codefly", pgidRootDirName)
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	foreignPath := filepath.Join(root, "foreign-contract.pgid")
	foreignRecord := []byte("foreign record contract\n")
	if err := os.WriteFile(foreignPath, foreignRecord, 0o600); err != nil {
		t.Fatal(err)
	}

	currentDir, err := pgidStateDir()
	if err != nil {
		t.Fatal(err)
	}
	if got, want := currentDir, filepath.Join(root, pgidRegistryNamespace); got != want {
		t.Fatalf("current registry = %q, want %q", got, want)
	}
	if err := ReapStaleProcessGroups(t.Context()); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(foreignPath)
	if err != nil {
		t.Fatalf("foreign record was moved or removed: %v", err)
	}
	if string(got) != string(foreignRecord) {
		t.Fatalf("foreign record changed: %q", got)
	}
	info, err := os.Stat(currentDir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o700); got != want {
		t.Fatalf("current registry permissions = %o, want %o", got, want)
	}
}
