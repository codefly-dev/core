package composition

import (
	"errors"

	"golang.org/x/sys/unix"
)

func activateProjectionLink(candidate, destination string) error {
	// Keep the superseded symlink linked while concurrent lookups finish.
	err := unix.Renameat2(unix.AT_FDCWD, candidate, unix.AT_FDCWD, destination, unix.RENAME_EXCHANGE)
	if !errors.Is(err, unix.ENOENT) {
		return err
	}
	err = unix.Renameat2(unix.AT_FDCWD, candidate, unix.AT_FDCWD, destination, unix.RENAME_NOREPLACE)
	if errors.Is(err, unix.EEXIST) {
		return unix.Renameat2(unix.AT_FDCWD, candidate, unix.AT_FDCWD, destination, unix.RENAME_EXCHANGE)
	}
	return err
}
