package shared

import (
	"io/fs"
	"os"
	"path"
)

type FileSystem interface {
	AbsoluteFile(f File) string
	AbsoluteDir(dir Dir) string
	ReadDir(dir Dir) ([]os.DirEntry, error)
	ReadFile(file File) ([]byte, error)
}

type S = string

/*
Embedded file system
*/

type FSReader struct {
	FS   fs.FS
	root string
}

func (fr *FSReader) AbsoluteFile(f File) string {
	return f.Relative()
}

func (fr *FSReader) AbsoluteDir(dir Dir) string {
	return dir.Relative()
}

func (fr *FSReader) Absolute(dir Dir) string {
	return dir.Relative()
}

func (fr *FSReader) ReadFile(f File) ([]byte, error) {
	return fs.ReadFile(fr.FS, path.Join(fr.root, f.Relative()))
}

func (fr *FSReader) ReadDir(dir Dir) ([]os.DirEntry, error) {
	return fs.ReadDir(fr.FS, path.Join(fr.root, dir.Relative()))
}

func Embed(fsys fs.FS) *FSReader {
	return &FSReader{FS: fsys}
}

func (fr *FSReader) At(root string) *FSReader {
	fr.root = root
	return fr
}

func (fr *FSReader) Copy(s string, file string) error {
	content, err := fr.ReadFile(NewFile(path.Join(fr.root, s)))
	if err != nil {
		return err
	}
	err = os.WriteFile(file, content, 0600)
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

func (dr *DirReader) AbsoluteFile(f File) string {
	return f.Relative()
}

func (dr *DirReader) AbsoluteDir(dir Dir) string {
	return dir.Relative()
}

func NewDirReader() *DirReader {
	return &DirReader{}
}

func (dr *DirReader) At(root string) *DirReader {
	dr.root = root
	return dr
}

func (dr *DirReader) ReadDir(dir Dir) ([]os.DirEntry, error) {
	return os.ReadDir(path.Join(dr.root, dir.Relative()))
}

func (dr *DirReader) ReadFile(f File) ([]byte, error) {
	content, err := os.ReadFile(path.Join(dr.root, f.Relative()))
	if err != nil {
		return nil, err
	}
	return content, nil
}
