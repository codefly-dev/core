package composition

import (
	"errors"

	"golang.org/x/sys/unix"
)

func activateProjectionLink(candidate, destination string) error {
	// Darwin can return EINVAL to an in-flight pathname lookup when its
	// symlink is unlinked. Exchange names and retain the superseded inode,
	// just as projection revisions remain available to their readers.
	err := unix.RenamexNp(candidate, destination, unix.RENAME_SWAP)
	if !errors.Is(err, unix.ENOENT) {
		return err
	}
	err = unix.RenamexNp(candidate, destination, unix.RENAME_EXCL)
	if errors.Is(err, unix.EEXIST) {
		return unix.RenamexNp(candidate, destination, unix.RENAME_SWAP)
	}
	return err
}
