//go:build unix

package resources

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"syscall"
)

// InspectStorageFilesystem observes the filesystem containing root. root may
// name a not-yet-created application-state directory; in that case the nearest
// existing ancestor determines the target filesystem.
func InspectStorageFilesystem(root string) (StorageFilesystem, error) {
	ancestor, err := existingStorageAncestor(root)
	if err != nil {
		return StorageFilesystem{}, err
	}
	var state syscall.Statfs_t
	if err = syscall.Statfs(ancestor, &state); err != nil {
		return StorageFilesystem{}, fmt.Errorf("inspect storage filesystem: %w", err)
	}
	if state.Bsize <= 0 {
		return StorageFilesystem{}, fmt.Errorf("storage filesystem reported invalid block size %d", state.Bsize)
	}
	blockSize := uint64(state.Bsize)
	totalBytes, ok := checkedStorageBytes(uint64(state.Blocks), blockSize)
	if !ok {
		return StorageFilesystem{}, errors.New("storage filesystem total byte count overflows uint64")
	}
	availableBytes, ok := checkedStorageBytes(uint64(state.Bavail), blockSize)
	if !ok {
		return StorageFilesystem{}, errors.New("storage filesystem available byte count overflows uint64")
	}
	scope, err := storageAuthorityScopeBytes()
	if err != nil {
		return StorageFilesystem{}, err
	}
	identity := fmt.Sprintf("%x:%v:%d", scope, state.Fsid, totalBytes)
	digest := sha256.Sum256([]byte(identity))
	return StorageFilesystem{
		AuthorityID:         "storage/sha256:" + hex.EncodeToString(digest[:16]),
		TotalBytes:          totalBytes,
		AvailableBytes:      availableBytes,
		AllocationUnitBytes: blockSize,
	}, nil
}
