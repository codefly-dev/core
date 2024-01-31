package shared

import (
	"io/fs"
	"os"
	"path"
)

type FileSystem interface {
	ReadDir(relativePath string) ([]os.DirEntry, error)
	ReadFile(relativePath string) ([]byte, error)
}

type S = string

/*
Embedded file system
*/

type FSReader struct {
	FS   fs.FS
	root string
}

func (fr *FSReader) ReadFile(relativePath string) ([]byte, error) {
	return fs.ReadFile(fr.FS, path.Join(fr.root, relativePath))
}

func (fr *FSReader) ReadDir(relativePath string) ([]os.DirEntry, error) {
	return fs.ReadDir(fr.FS, path.Join(fr.root, relativePath))
}

func Embed(fsys fs.FS) *FSReader {
	return &FSReader{FS: fsys}
}

func (fr *FSReader) At(root string) *FSReader {
	fr.root = root
	return fr
}

func (fr *FSReader) Copy(relativePath string, destination string) error {
	content, err := fr.ReadFile(path.Join(fr.root, relativePath))
	if err != nil {
		return err
	}
	err = os.WriteFile(destination, content, 0600)
	if err != nil {
		return err
	}
	return nil
}

/*
Local file system
*/

type DirReader struct {
	root string
}

func NewDirReader() *DirReader {
	return &DirReader{}
}

func (dr *DirReader) At(root string) *DirReader {
	dr.root = root
	return dr
}

func (dr *DirReader) ReadDir(relativePath string) ([]os.DirEntry, error) {
	return os.ReadDir(path.Join(dr.root, relativePath))
}

func (dr *DirReader) ReadFile(relativePath string) ([]byte, error) {
	content, err := os.ReadFile(path.Join(dr.root, relativePath))
	if err != nil {
		return nil, err
	}
	return content, nil
}
