// Package code: FileOperation abstracts VFS-only operations (read, write, list,
// delete, move, copy, search, replace). Used when code intelligence (LSP, Fix,
// symbols) is not needed and the virtual system / overlay should be the single
// source of truth.

package code

import (
	"context"
	"io/fs"
	"path/filepath"
)

// FileOperation provides VFS-only operations. All paths are relative to the
// root passed at construction. Search and ReplaceInFile run on the VFS (no
// ripgrep on disk), so overlay/virtual state is consistent.
type FileOperation interface {
	ReadFile(ctx context.Context, path string) ([]byte, error)
	WriteFile(ctx context.Context, path string, data []byte) error
	ListFiles(ctx context.Context, path string, recursive bool, extensions []string) ([]string, error)
	DeleteFile(ctx context.Context, path string) error
	MoveFile(ctx context.Context, oldPath, newPath string) error
	CopyFile(ctx context.Context, srcPath, destPath string) error
	Search(ctx context.Context, opts SearchOpts) (*SearchResult, error)
	ReplaceInFile(ctx context.Context, path, find, replace string) (changed bool, err error)
}

// fileOps implements FileOperation using a VFS and root. Paths are root-relative.
type fileOps struct {
	vfs  VFS
	root string
}

// NewFileOps returns a FileOperation that uses vfs with all paths under root.
// Paths passed to its methods must be relative to root (e.g. "foo/bar.go").
func NewFileOps(vfs VFS, root string) FileOperation {
	return &fileOps{vfs: vfs, root: root}
}

func (f *fileOps) abs(path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(f.root, path)
}

func (f *fileOps) ReadFile(ctx context.Context, path string) ([]byte, error) {
	return f.vfs.ReadFile(f.abs(path))
}

func (f *fileOps) WriteFile(ctx context.Context, path string, data []byte) error {
	abs := f.abs(path)
	if err := f.vfs.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return f.vfs.WriteFile(abs, data, 0o644)
}

func (f *fileOps) ListFiles(ctx context.Context, path string, recursive bool, extensions []string) ([]string, error) {
	base := f.root
	if path != "" {
		base = filepath.Join(f.root, path)
	}
	extSet := make(map[string]bool)
	for _, e := range extensions {
		if e != "" && e[0] != '.' {
			e = "." + e
		}
		extSet[e] = true
	}
	var out []string
	walkFn := func(p string, d interface{ IsDir() bool; Name() string }, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && d.Name() != "" && d.Name()[0] == '.' && p != base {
			return fs.SkipDir
		}
		if !recursive && d.IsDir() && p != base {
			return fs.SkipDir
		}
		rel, _ := filepath.Rel(f.root, p)
		if rel == "." {
			return nil
		}
		if len(extSet) > 0 && !d.IsDir() && !extSet[filepath.Ext(p)] {
			return nil
		}
		if !d.IsDir() {
			out = append(out, rel)
		}
		return nil
	}
	err := f.vfs.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		return walkFn(p, d, err)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (f *fileOps) DeleteFile(ctx context.Context, path string) error {
	return f.vfs.Remove(f.abs(path))
}

func (f *fileOps) MoveFile(ctx context.Context, oldPath, newPath string) error {
	oldAbs := f.abs(oldPath)
	newAbs := f.abs(newPath)
	if err := f.vfs.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return err
	}
	return f.vfs.Rename(oldAbs, newAbs)
}

func (f *fileOps) CopyFile(ctx context.Context, srcPath, destPath string) error {
	data, err := f.vfs.ReadFile(f.abs(srcPath))
	if err != nil {
		return err
	}
	return f.WriteFile(ctx, destPath, data)
}

func (f *fileOps) Search(ctx context.Context, opts SearchOpts) (*SearchResult, error) {
	return SearchVFS(ctx, f.vfs, f.root, opts)
}

func (f *fileOps) ReplaceInFile(ctx context.Context, path, find, replace string) (changed bool, err error) {
	data, err := f.vfs.ReadFile(f.abs(path))
	if err != nil {
		return false, err
	}
	result := SmartEdit(string(data), find, replace)
	if !result.OK {
		return false, nil
	}
	if err := f.WriteFile(ctx, path, []byte(result.Content)); err != nil {
		return false, err
	}
	return true, nil
}
