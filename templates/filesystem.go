package templates

import (
	"io/fs"
	"os"

	"github.com/codefly-dev/core/shared"
)

type FileSystem interface {
	AbsoluteFile(f shared.File) string
	AbsoluteDir(dir shared.Dir) string
	ReadDir(dir shared.Dir) ([]os.DirEntry, error)
	ReadFile(file shared.File) ([]byte, error)
}

type S = string

/*
Embedded file system
*/

type FSReader struct {
	FS fs.FS
}

func (fr *FSReader) AbsoluteFile(f shared.File) string {
	return f.Relative()
}

func (fr *FSReader) AbsoluteDir(dir shared.Dir) string {
	return dir.Relative()
}

func (fr *FSReader) Absolute(dir shared.Dir) string {
	return dir.Relative()
}

func (fr *FSReader) ReadFile(file shared.File) ([]byte, error) {
	return fs.ReadFile(fr.FS, file.Relative())
}

func (fr *FSReader) ReadDir(dir shared.Dir) ([]os.DirEntry, error) {
	logger := shared.NewLogger("templates.FSReader.ReadNewDir<%s>", dir)
	logger.Tracef("new")
	return fs.ReadDir(fr.FS, dir.Relative())
}

func NewEmbeddedFileSystem(fsys fs.FS) *FSReader {
	return &FSReader{FS: fsys}
}

func (fr *FSReader) Copy(s string, file string) error {
	content, err := fr.ReadFile(shared.File{})
	if err != nil {
		return err
	}
	err = os.WriteFile(file, content, 0o644)
	if err != nil {
		return err
	}
	return nil
}

/*
Local file system
*/

type DirReader struct{}

func (dr *DirReader) AbsoluteFile(f shared.File) string {
	return f.Relative()
}

func (dr *DirReader) AbsoluteDir(dir shared.Dir) string {
	return dir.Relative()
}

func NewDirReader() *DirReader {
	return &DirReader{}
}

func (dr *DirReader) ReadDir(dir shared.Dir) ([]os.DirEntry, error) {
	return os.ReadDir(dir.Relative())
}

func (dr *DirReader) ReadFile(file shared.File) ([]byte, error) {
	content, err := os.ReadFile(file.Relative())
	if err != nil {
		return nil, err
	}
	return content, nil
}
