package services

import "golang.org/x/sys/unix"

func exchangeWorkloadTree(staging, destination string) error {
	return unix.RenamexNp(staging, destination, unix.RENAME_SWAP)
}
