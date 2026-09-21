//go:build unix

package composition

import (
	"io"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

func TestExecutionOutputOpenRejectsFIFOReplacement(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "output")
	require.NoError(t, os.WriteFile(path, []byte("rendered"), 0o600))
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	info, err := root.Stat("output")
	require.NoError(t, err)
	require.True(t, info.Mode().IsRegular())

	fifo := filepath.Join(directory, "fifo")
	require.NoError(t, syscall.Mkfifo(fifo, 0o600))
	require.NoError(t, os.Rename(fifo, path))
	done := make(chan error, 1)
	go func() {
		file, openErr := openExecutionOutput(root, "output")
		if file != nil {
			_ = file.Close()
		}
		done <- openErr
	}()
	select {
	case err = <-done:
		require.ErrorContains(t, err, "execution output must be a regular file")
	case <-time.After(time.Second):
		// Release a regressed blocking open before failing, leaving no worker.
		peer, peerErr := os.OpenFile(path, os.O_RDWR|syscall.O_NONBLOCK, 0)
		require.NoError(t, peerErr)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Error("FIFO peer did not release output opener")
		}
		require.NoError(t, peer.Close())
		t.Fatal("output replacement blocked descriptor validation waiting for a FIFO writer")
	}
}

func TestExecutionOutputOpenRetainsReadOnlyContainment(t *testing.T) {
	directory := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(directory, "output"), []byte("rendered"), 0o600))
	root, err := os.OpenRoot(directory)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	file, err := openExecutionOutput(root, "output")
	require.NoError(t, err)
	flags, err := unix.FcntlInt(file.Fd(), unix.F_GETFL, 0)
	require.NoError(t, err)
	require.NotZero(t, flags&syscall.O_NONBLOCK)
	data, err := io.ReadAll(file)
	require.NoError(t, err)
	require.Equal(t, "rendered", string(data))
	_, err = file.Write([]byte("modified"))
	require.Error(t, err)
	require.NoError(t, file.Close())
	outside := filepath.Join(t.TempDir(), "outside")
	require.NoError(t, os.WriteFile(outside, []byte("outside"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(directory, "escape")))
	for _, name := range []string{"escape", "../outside", ".", "missing"} {
		file, err = openExecutionOutput(root, name)
		require.Error(t, err, name)
		require.Nil(t, file, name)
	}
}
