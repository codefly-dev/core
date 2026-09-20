package composition

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProjectionActivationPreservesConcurrentReaders(t *testing.T) {
	root := t.TempDir()
	for _, revision := range []string{"one", "two"} {
		require.NoError(t, os.Mkdir(filepath.Join(root, revision), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, revision, "file"), []byte(revision), 0o444))
	}
	destination := filepath.Join(root, "active")
	initial := filepath.Join(root, "initial")
	require.NoError(t, os.Symlink("one", initial))
	require.NoError(t, activateProjectionLink(initial, destination))
	stop := make(chan struct{})
	errors := make(chan error, 4)
	var readers sync.WaitGroup
	for range 4 {
		readers.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					data, err := os.ReadFile(filepath.Join(destination, "file"))
					if err != nil {
						errors <- err
						return
					}
					if string(data) != "one" && string(data) != "two" {
						errors <- fmt.Errorf("unexpected revision content: %q", data)
						return
					}
				}
			}
		})
	}
	t.Cleanup(func() {
		close(stop)
		readers.Wait()
		close(errors)
		for err := range errors {
			t.Error(err)
		}
	})
	for n := range 500 {
		candidate := filepath.Join(root, fmt.Sprintf("candidate-%d", n))
		require.NoError(t, os.Symlink([]string{"one", "two"}[n%2], candidate))
		require.NoError(t, activateProjectionLink(candidate, destination))
	}
}
