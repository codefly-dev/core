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

//
//func CopyFileWithReplacement(from string, to string, replacements []Replacement) error {
//	logger := NewLogger().With("builder.CopyFile<%s><%s>", from, to)
//	// Open source file for reading
//	src, err := os.ReadFile(from)
//	if err != nil {
//		return logger.Wrapf(err, "cannot open file")
//	}
//
//	dst := fmt.Sprintf("%s.tmpl", to)
//	dstFile, err := os.Create(dst)
//	if err != nil {
//		return logger.Wrapf(err, "cannot create file")
//	}
//	defer dstFile.Close()
//	original := string(src)
//	for _, rpl := range replacements {
//		original = strings.Replace(original, rpl.From, rpl.To, -1)
//	}
//	_, err = dstFile.WriteString(original)
//	if err != nil {
//		return logger.Wrapf(err, "cannot write file")
//	}
//	return nil
//}
//
//// CopyDirectory recursively copies a directory
//func CopyDirectory(ctx context.Context, src, dst string) error {
//	logger := NewLogger().With("CopyDirectory<%s><%s>", src, dst)
//	// Check if source directory exists
//	if err := CheckDirectory(src); err != nil {
//		return logger.Wrapf(err, "source directory does not exist")
//	}
//
//	// Check if destination directory exists
//	if err := CheckDirectoryOrCreate(ctx, dst); err != nil {
//		return logger.Wrapf(err, "destination directory does not exist")
//	}
//
//	// Read the source directory contents
//	contents, err := os.ReadDir(src)
//	if err != nil {
//		return logger.Wrapf(err, "cannot read source directory")
//	}
//
//	// Loop through the contents
//	for _, content := range contents {
//		// Construct the source and destination paths
//		srcPath := filepath.Join(src, content.Name())
//		dstPath := filepath.Join(dst, content.Name())
//
//		// If the content is a directory, recursively copy it
//		if content.IsDir() {
//			if err := CopyDirectory(ctx, srcPath, dstPath); err != nil {
//				return logger.Wrapf(err, "cannot copy directory")
//			}
//		} else {
//			// Otherwise, copy the file
//			if err := CopyFile(ctx, srcPath, dstPath); err != nil {
//				return logger.Wrapf(err, "cannot copy file")
//			}
//		}
//	}
//
//	return nil
//}
//
//// GenerateTree recursively generates a string representation of the directory tree
//func GenerateTree(p, indent string) (string, error) {
//	// Read the directory contents
//	contents, err := os.ReadDir(p)
//	if err != nil {
//		return "", err
//	}
//
//	// Loadialize the tree string
//	var treeStr string
//
//	// Loop through the contents
//	for i, content := range contents {
//		// Expose the content name to the tree string
//		treeStr += fmt.Sprintf("%s|-- %s\n", indent, content.Name())
//
//		// If the content is a directory, recursively generate its tree string
//		if content.IsDir() {
//			subTree, err := GenerateTree(filepath.Join(p, content.Name()), indent+"    ")
//			if err != nil {
//				return "", err
//			}
//			treeStr += subTree
//		}
//
//		// If it's the last item, adjust the indent
//		if i == len(contents)-1 {
//			treeStr += fmt.Sprintf("%s|\n", indent)
//		}
//	}
//
//	return treeStr, nil
//}
