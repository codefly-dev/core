//go:build !unix

package resources

import (
	"fmt"
	"runtime"
)

// InspectStorageFilesystem is a Unix-only observer: it depends on the statfs
// syscall, which platforms outside the `unix` build constraint (e.g. Windows)
// do not provide. Cross-compiled builds must still link, so the capability
// reports itself unavailable at run time instead of failing to compile.
func InspectStorageFilesystem(root string) (StorageFilesystem, error) {
	return StorageFilesystem{}, fmt.Errorf("inspect storage filesystem: unsupported on %s", runtime.GOOS)
}
