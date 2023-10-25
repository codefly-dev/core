package shared

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
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
	logger := NewLogger("shared.File.RelativeFrom<%s><%s>", f.file, base)
	rel, err := filepath.Rel(base.name, f.file)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot get relative path")
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
	logger := NewLogger("shared.dir.RelativeFrom<%s><%s>", d, base)
	rel, err := filepath.Rel(base.Absolute(), d.Absolute())
	if err != nil {
		return nil, logger.Wrapf(err, "cannot get relative path")
	}
	return &Dir{name: rel}, nil
}

func (d *Dir) Join(other Dir) Dir {
	return Dir{name: filepath.Join(d.name, other.name)}
}

func (d *Dir) Absolute() string {
	return d.name
}

func NewDir(dir string) Dir {
	return Dir{name: dir}
}

func Local(dir string) (*Dir, error) {
	logger := NewLogger("shared.Local<%s>", dir)
	cur, err := os.Getwd()
	if err != nil {
		return nil, logger.Wrapf(err, "cannot get current directory")
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

func FileExists(p string) bool {
	_, err := os.Stat(p)
	return !os.IsNotExist(err)
}

type CopyInstruction struct {
	Name string
	Path string
}

// CheckDirectory checks if a directory exists
// otherwise returns an error
func CheckDirectory(path string) error {
	logger := NewLogger("shared.CheckDirectory<%s>", path)
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return logger.Wrapf(err, "directory does not exist")
		}
		return logger.Wrapf(err, "check directory existence") // Some other error occurred
	}

	// Check if it's actually a directory
	if !info.IsDir() {
		return logger.Errorf("%s is not a directory", path)
	}
	return nil
}

func CreateDirIf(path string) error {
	if CheckDirectory(path) != nil {
		return os.MkdirAll(path, 0755)
	}
	return nil
}

func CheckEmptyDirectoryOrCreate(path string) error {
	logger := NewLogger("shared.CheckEmptyDirectoryOrCreate<%s>", path)
	err := CheckDirectoryOrCreate(path)
	if err != nil {
		return logger.Wrapf(err, "cannot check or create directory")
	}
	// Check if directory is empty
	files, err := os.ReadDir(path)
	if err != nil {
		return logger.Wrapf(err, "cannot read directory")
	}
	if len(files) > 0 {
		return logger.Errorf("exists and not empty")
	}
	return nil

}

func CheckDirectoryOrCreate(path string) error {
	logger := NewLogger("shared.CheckEmptyDirectoryOrCreate<%s>", path)
	logger.Tracef("Checking for empty or create")
	// Check if directory exists
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(path, 0755)
			if err != nil {
				return logger.Wrapf(err, "cannot create directory")
			}
		}
		return logger.Wrapf(err, "cannot create directory") // Some other error occurred
	}

	// Check if it's actually a directory
	if !info.IsDir() {
		return logger.Errorf("%s is not a directory", path)
	}
	return nil

}

func CopyFile(from string, to string) error {
	logger := NewLogger("builder.CopyFile<%s><%s>", from, to)
	// Open source file for reading
	srcFile, err := os.Open(from)
	if err != nil {
		return logger.Wrapf(err, "cannot open file")
	}
	defer srcFile.Close()

	if err := os.MkdirAll(filepath.Dir(to), 0755); err != nil {
		log.Fatalf("Failed to create directory: %v", err)
	}
	dstFile, err := os.Create(to)
	if err != nil {
		return logger.Wrapf(err, "cannot create file")
	}
	defer dstFile.Close()

	// Copy the contents of the source file to the destination file
	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		return logger.Wrapf(err, "cannot copy file")
	}
	return nil
}

type Replacement struct {
	From string `yaml:"from"`
	To   string `yaml:"to"`
}

func CopyFileWithReplacement(from string, to string, replacements []Replacement) error {
	logger := NewLogger("builder.CopyFile<%s><%s>", from, to)
	// Open source file for reading
	src, err := os.ReadFile(from)
	if err != nil {
		return logger.Wrapf(err, "cannot open file")
	}

	dst := fmt.Sprintf("%s.tmpl", to)
	dstFile, err := os.Create(dst)
	if err != nil {
		return logger.Wrapf(err, "cannot create file")
	}
	defer dstFile.Close()
	original := string(src)
	for _, rpl := range replacements {
		original = strings.Replace(original, rpl.From, rpl.To, -1)
	}
	_, err = dstFile.WriteString(original)
	if err != nil {
		return logger.Wrapf(err, "cannot write file")
	}
	return nil
}

// CopyDirectory recursively copies a directory
func CopyDirectory(src, dst string) error {
	logger := NewLogger("shared.Copyshared.Directory<%s><%s>", src, dst)
	// Check if source directory exists
	if err := CheckDirectory(src); err != nil {
		return logger.Wrapf(err, "source directory does not exist")
	}

	// Check if destination directory exists
	if err := CheckDirectoryOrCreate(dst); err != nil {
		return logger.Wrapf(err, "destination directory does not exist")
	}

	// Read the source directory contents
	contents, err := os.ReadDir(src)
	if err != nil {
		return logger.Wrapf(err, "cannot read source directory")
	}

	// Loop through the contents
	for _, content := range contents {
		// Construct the source and destination paths
		srcPath := filepath.Join(src, content.Name())
		dstPath := filepath.Join(dst, content.Name())

		// If the content is a directory, recursively copy it
		if content.IsDir() {
			if err := CopyDirectory(srcPath, dstPath); err != nil {
				return logger.Wrapf(err, "cannot copy directory")
			}
		} else {
			// Otherwise, copy the file
			if err := CopyFile(srcPath, dstPath); err != nil {
				return logger.Wrapf(err, "cannot copy file")
			}
		}
	}

	return nil
}

// GenerateTree recursively generates a string representation of the directory tree
func GenerateTree(p, indent string) (string, error) {
	// Read the directory contents
	contents, err := os.ReadDir(p)
	if err != nil {
		return "", err
	}

	// Initialize the tree string
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
