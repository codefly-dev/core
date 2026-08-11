package resources

// ARCHITECTURE: Filesystem capacity belongs to the execution/storage boundary,
// not to an orchestrator. This observer gives Codefly hosts and an in-process
// application backend one opaque physical-authority identity without exposing
// paths. A process scope prevents unrelated remote hosts with similar device
// numbers from being coalesced accidentally, while local library users share
// the same scope and can combine demands on one physical volume.

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

// StorageFilesystem is a path-free point-in-time capacity observation. The
// AuthorityID is comparable only within the current reporting process.
type StorageFilesystem struct {
	AuthorityID         string
	TotalBytes          uint64
	AvailableBytes      uint64
	AllocationUnitBytes uint64
}

// StorageTree is a conservative storage requirement for materializing one
// existing filesystem tree on the same authority. RequiredBytes rounds every
// entry to the authority's allocation unit, so tiny files and directories do
// not disappear from admission estimates. Sparse files and copy-on-write
// clones are deliberately charged at their logical size: the requirement must
// remain safe when the next materialization cannot preserve those optimizations.
type StorageTree struct {
	RequiredBytes uint64
	EntryCount    uint64
}

var storageAuthorityScope struct {
	once  sync.Once
	value [16]byte
	err   error
}

// InspectStorageFilesystem observes the filesystem containing root. root may
// name a not-yet-created application-state directory; in that case the nearest
// existing ancestor determines the target filesystem.
func InspectStorageFilesystem(root string) (StorageFilesystem, error) {
	ancestor, err := existingStorageAncestor(root)
	if err != nil {
		return StorageFilesystem{}, err
	}
	var state syscall.Statfs_t
	if err := syscall.Statfs(ancestor, &state); err != nil {
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

// InspectStorageTree measures an existing tree through filesystem metadata.
// It never reads file contents and never follows symlinks. Callers must invoke
// it only at the execution/storage boundary that owns the inspected tree.
func InspectStorageTree(ctx context.Context, root string) (StorageTree, error) {
	filesystem, err := InspectStorageFilesystem(root)
	if err != nil {
		return StorageTree{}, err
	}
	var tree StorageTree
	err = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("inspect storage tree entry: %w", err)
		}
		size := uint64(1)
		if info.Size() > 0 {
			size = uint64(info.Size())
		}
		allocated, ok := roundedStorageBytes(size, filesystem.AllocationUnitBytes)
		if !ok || math.MaxUint64-tree.RequiredBytes < allocated {
			return errors.New("storage tree byte count overflows uint64")
		}
		tree.RequiredBytes += allocated
		if tree.EntryCount == math.MaxUint64 {
			return errors.New("storage tree entry count overflows uint64")
		}
		tree.EntryCount++
		return nil
	})
	if err != nil {
		return StorageTree{}, fmt.Errorf("inspect storage tree: %w", err)
	}
	return tree, nil
}

func existingStorageAncestor(root string) (string, error) {
	canonical := strings.TrimSpace(root)
	if canonical == "" {
		return "", errors.New("storage filesystem root is required")
	}
	absolute, err := filepath.Abs(filepath.Clean(canonical))
	if err != nil {
		return "", fmt.Errorf("resolve storage filesystem root: %w", err)
	}
	for candidate := absolute; ; candidate = filepath.Dir(candidate) {
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("inspect storage filesystem ancestor: %w", err)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			return "", fmt.Errorf("storage filesystem root %q has no existing ancestor", root)
		}
	}
}

func storageAuthorityScopeBytes() ([16]byte, error) {
	storageAuthorityScope.once.Do(func() {
		_, storageAuthorityScope.err = rand.Read(storageAuthorityScope.value[:])
		if storageAuthorityScope.err != nil {
			storageAuthorityScope.err = fmt.Errorf("initialize storage authority scope: %w", storageAuthorityScope.err)
		}
	})
	return storageAuthorityScope.value, storageAuthorityScope.err
}

func checkedStorageBytes(blocks, blockSize uint64) (uint64, bool) {
	if blockSize != 0 && blocks > math.MaxUint64/blockSize {
		return 0, false
	}
	return blocks * blockSize, true
}

func roundedStorageBytes(size, allocationUnit uint64) (uint64, bool) {
	if allocationUnit == 0 {
		return 0, false
	}
	remainder := size % allocationUnit
	if remainder == 0 {
		return size, true
	}
	addition := allocationUnit - remainder
	if size > math.MaxUint64-addition {
		return 0, false
	}
	return size + addition, true
}
