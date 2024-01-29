package shared

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/codefly-dev/core/wool"
)

type File struct {
	file string
}

func (f *File) Relative() string {
	return f.file
}

func NewFile(file string) File {
	return File{file: file}
}

func (f *File) RelativeFrom(base Dir) (*File, error) {
	rel, err := filepath.Rel(base.name, f.file)
	if err != nil {
		return nil, err
	}
	return &File{file: rel}, nil
}

func (f *File) Base() string {
	return filepath.Base(f.file)
}

func (f *File) RelativePath() string {
	return filepath.Dir(f.file)
}

type Dir struct {
	name string
}

func (d *Dir) Relative() string {
	return d.name
}

func (d *Dir) RelativeFrom(base Dir) (*Dir, error) {
	rel, err := filepath.Rel(base.Absolute(), d.Absolute())
	if err != nil {
		return nil, err
	}
	return &Dir{name: rel}, nil
}

func (d *Dir) Join(other Dir) Dir {
	return Dir{name: filepath.Join(d.name, other.name)}
}

func (d *Dir) Absolute() string {
	return d.name
}

func NewDir(dir string, args ...any) Dir {
	return Dir{name: fmt.Sprintf(dir, args...)}
}

func Local(dir string) (*Dir, error) {
	cur, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	return &Dir{name: filepath.Join(cur, dir)}, nil
}

func MustLocal(dir string) Dir {
	d, err := Local(dir)
	if err != nil {
		panic(err)
	}
	return *d
}

func FileExists(file string) bool {
	info, err := os.Stat(file)
	return !os.IsNotExist(err) && !info.IsDir()
}

func DirectoryExists(p string) bool {
	info, err := os.Stat(p)
	return !os.IsNotExist(err) && info.IsDir()
}

type CopyInstruction struct {
	Name string
	Path string
}

// CheckDirectory is a safer version of DirectoryExists
// return bool, err
// err only for unexpected behavior
func CheckDirectory(ctx context.Context, dir string) (bool, error) {
	w := wool.Get(ctx).In("shared.CheckDirectory", wool.Field("dir", dir))
	info, err := os.Stat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, w.Wrapf(err, "cannot check directory")
	}

	// Check if it's actually a directory
	if !info.IsDir() {
		return false, nil
	}
	return true, nil
}

// CheckEmptyDirectoryOrCreate checks if a directory exists and is empty
// bool if created
// err only for unexpected behavior or if exists
func CheckEmptyDirectoryOrCreate(ctx context.Context, dir string) (bool, error) {
	w := wool.Get(ctx).In("shared.CheckEmptyDirectoryOrCreate", wool.DirField(dir))
	exists, err := CheckDirectory(ctx, dir)
	if err != nil {
		return CheckDirectoryOrCreate(ctx, dir)
	}
	if !exists {
		return false, nil
	}
	// Check if directory is empty
	files, err := os.ReadDir(dir)
	if err != nil {
		return false, w.Wrapf(err, "cannot read directory")
	}
	if len(files) > 0 {
		return false, nil
	}
	return true, nil
}

func DeleteFile(ctx context.Context, file string) error {
	// Do nothing if not present
	if !FileExists(file) {
		return nil
	}
	w := wool.Get(ctx).In("shared.DeleteFile", wool.FileField(file))
	err := os.Remove(file)
	if err != nil {
		return w.Wrapf(err, "cannot delete file")
	}
	return nil
}

func EmptyDir(dir string) error {
	// Do nothing if not present
	if !DirectoryExists(dir) {
		return nil
	}
	// Check if directory is empty
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, file := range files {
		err := os.RemoveAll(filepath.Join(dir, file.Name()))
		if err != nil {
			return err
		}
	}
	return nil

}

// CheckDirectoryOrCreate checks if a directory exists or create it if it doesn't
// bool: created
// err: only for unexpected behavior
func CheckDirectoryOrCreate(ctx context.Context, dir string) (bool, error) {
	w := wool.Get(ctx).In("shared.CheckDirectoryOrCreate", wool.Field("dir", dir))
	exists, err := CheckDirectory(ctx, dir)
	if err != nil {
		return false, err
	}
	if exists {
		return false, nil
	}

	err = os.MkdirAll(dir, 0755)
	if err != nil {
		return false, w.Wrapf(err, "cannot create directory")
	}
	return true, nil
}

func CopyFile(_ context.Context, from string, to string) error {
	w := wool.Get(context.Background()).In("shared.CopyFile")
	// Open source file for reading
	srcFile, err := os.Open(from)
	if err != nil {
		return w.With(wool.FileField(from)).Wrapf(err, "cannot open file")
	}
	defer func(srcFile *os.File) {
		err := srcFile.Close()
		if err != nil {
			w.Error("cannot close file", wool.ErrField(err))

		}
	}(srcFile)

	dir := filepath.Dir(to)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return w.With(wool.DirField(dir)).Wrapf(err, "Failed to create directory")
	}
	dstFile, err := os.Create(to)
	if err != nil {
		return w.With(wool.FileField(to)).Wrapf(err, "cannot create file")
	}
	defer func(dstFile *os.File) {
		err := dstFile.Close()
		if err != nil {
			w.Error("cannot close file", wool.ErrField(err))
		}
	}(dstFile)

	// Copy the contents of the source file to the destination file
	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return w.Wrapf(err, "cannot copy file", wool.Field("from", from), wool.Field("to", to))
	}
	return nil
}

type Replacement struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

// GenerateTree recursively generates a string representation of the directory tree
func GenerateTree(p, indent string) (string, error) {
	// Read the directory contents
	contents, err := os.ReadDir(p)
	if err != nil {
		return "", err
	}

	// Loadialize the tree string
	var treeStr string

	// Loop through the contents
	for i, content := range contents {
		// Expose the content name to the tree string
		treeStr += fmt.Sprintf("%s|-- %s\n", indent, content.Name())

		// If the content is a directory, recursively generate its tree string
		if content.IsDir() {
			subTree, err := GenerateTree(filepath.Join(p, content.Name()), indent+"    ")
			if err != nil {
				return "", err
			}
			treeStr += subTree
		}

		// If it's the last item, adjust the indent
		if i == len(contents)-1 {
			treeStr += fmt.Sprintf("%s|\n", indent)
		}
	}

	return treeStr, nil
}
