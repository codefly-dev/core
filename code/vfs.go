package code

import (
	"io/fs"
	"os"
	"path/filepath"
)

// VFS abstracts filesystem operations so DefaultCodeServer can work with
// local disk, in-memory stores, overlay layers, or remote backends.
// All paths are absolute -- the server joins SourceDir + relative before calling.
type VFS interface {
	ReadFile(path string) ([]byte, error)
	WriteFile(path string, data []byte, perm os.FileMode) error
	Remove(path string) error
	Rename(oldpath, newpath string) error
	Stat(path string) (os.FileInfo, error)
	MkdirAll(path string, perm os.FileMode) error
	WalkDir(root string, fn fs.WalkDirFunc) error
	ReadDir(path string) ([]os.DirEntry, error)
}

// LocalVFS delegates every call to the os/filepath standard library.
// This is the default for DefaultCodeServer -- zero behavior change from
// the original direct-os implementation.
type LocalVFS struct{}

func (LocalVFS) ReadFile(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func (LocalVFS) WriteFile(path string, data []byte, perm os.FileMode) error {
	return os.WriteFile(path, data, perm)
}

func (LocalVFS) Remove(path string) error {
	return os.Remove(path)
}

func (LocalVFS) Rename(oldpath, newpath string) error {
	return os.Rename(oldpath, newpath)
}

func (LocalVFS) Stat(path string) (os.FileInfo, error) {
	return os.Stat(path)
}

func (LocalVFS) MkdirAll(path string, perm os.FileMode) error {
	return os.MkdirAll(path, perm)
}

func (LocalVFS) WalkDir(root string, fn fs.WalkDirFunc) error {
	return filepath.WalkDir(root, fn)
}

func (LocalVFS) ReadDir(path string) ([]os.DirEntry, error) {
	return os.ReadDir(path)
}
