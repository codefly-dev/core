package composition

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/gofrs/flock"
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

func TestConcurrentProjectionPromotion(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { require.NoError(t, removeCacheTree(root)) })
	lock := validLock()
	destination := filepath.Join(root, "active")
	var stages []string
	for n := range 12 {
		stage := filepath.Join(root, fmt.Sprintf("stage-%d", n))
		require.NoError(t, os.Mkdir(stage, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(stage, "file"), []byte("complete"), 0o644))
		require.NoError(t, writeProjectionMetadata(stage, lock, &Catalog{}))
		stages = append(stages, stage)
	}
	start := make(chan struct{})
	results := make(chan error, len(stages))
	for _, stage := range stages {
		go func() {
			<-start
			results <- promoteProjection(t.Context(), stage, destination, lock)
		}()
	}
	close(start)
	for range stages {
		if err := <-results; err != nil {
			t.Error(err)
		}
	}
	require.True(t, projectionMatches(destination, lock))
	require.Equal(t, "complete", readFile(t, filepath.Join(destination, "file")))
}

func TestProjectionPromotionCancellationPreservesActiveRevision(t *testing.T) {
	root := t.TempDir()
	t.Cleanup(func() { require.NoError(t, removeCacheTree(root)) })
	lock := validLock()
	destination := filepath.Join(root, "active")
	for _, name := range []string{"initial", "candidate"} {
		stage := filepath.Join(root, name)
		require.NoError(t, os.Mkdir(stage, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(stage, "file"), []byte(name), 0o644))
		require.NoError(t, writeProjectionMetadata(stage, lock, &Catalog{}))
	}
	require.NoError(t, promoteProjection(t.Context(), filepath.Join(root, "initial"), destination, lock))
	held := flock.New(filepath.Join(root, ".revisions", ".promotion.lock"))
	require.NoError(t, held.Lock())
	t.Cleanup(func() { require.NoError(t, held.Close()) })
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := promoteProjection(ctx, filepath.Join(root, "candidate"), destination, lock)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, "initial", readFile(t, filepath.Join(destination, "file")))
	require.Equal(t, "candidate", readFile(t, filepath.Join(root, "candidate", "file")))
	require.NoError(t, held.Close())
	require.NoError(t, promoteProjection(t.Context(), filepath.Join(root, "candidate"), destination, lock))
	require.Equal(t, "candidate", readFile(t, filepath.Join(destination, "file")))
}
