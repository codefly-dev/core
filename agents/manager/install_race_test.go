package manager

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

// TestConcurrentInstallsOfOneReleaseNeverHideItsRecord holds the property
// several loads of one agent rely on: while the same release is installed over
// itself (every service sharing an agent, on a cold cache), a reader never
// finds the executable without its record, nor a partial record — so admission
// never mistakes the release for a local build.
func TestConcurrentInstallsOfOneReleaseNeverHideItsRecord(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "service-peer__1.0.0")
	payload := []byte("the release's executable")
	sum := sha256.Sum256(payload)
	digest := fmt.Sprintf("%x", sum[:])

	install := func() error {
		staged, err := os.CreateTemp(dir, ".agent-install-*")
		if err != nil {
			return err
		}
		defer os.Remove(staged.Name())
		if _, err := staged.Write(payload); err != nil {
			return err
		}
		if err := staged.Close(); err != nil {
			return err
		}
		return installRecordedExecutable(staged.Name(), target, digest)
	}
	if err := install(); err != nil {
		t.Fatal(err)
	}

	var stop atomic.Bool
	var installers sync.WaitGroup
	for range 2 {
		installers.Add(1)
		go func() {
			defer installers.Done()
			for !stop.Load() {
				if err := install(); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	for i := 0; i < 2000; i++ {
		recorded, err := readReleaseDigest(target)
		if err != nil || recorded != digest {
			stop.Store(true)
			installers.Wait()
			t.Fatalf("read %d: record %q, err %v; want %s throughout", i, recorded, err, digest)
		}
	}
	stop.Store(true)
	installers.Wait()
}
