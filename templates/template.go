package templates

import (
	"bytes"
	"fmt"
	"github.com/codefly-dev/core/shared"
	"os"
	"path"
	"strings"
	"text/template"
)

// ApplyTemplate takes a YAML template as []byte, populates it using data, and returns the result as a string.
func ApplyTemplate(t string, data any) (string, error) {
	tmpl, err := template.New("template").Parse(t)
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("cannot execute template: %w", err)
	}

	return buf.String(), nil
}

//
//func LoadTemplate(local embed.FS, f string, obj any) (string, error) {
//	// Read the file from the embedded file system
//	data, err := fs.ReadFile(local, fmt.Sprintf("templates/%s", f))
//	if err != nil {
//		return "", fmt.Errorf("could not read file: %v", err)
//	}
//	return ApplyTemplate(string(data), obj)
//}

func Walk(logger shared.BaseLogger, fs FileSystem, root shared.Dir, files *[]shared.File, dirs *[]shared.Dir) error {
	entries, err := fs.ReadDir(root)
	if err != nil {
		return logger.Wrapf(err, "cannot got to target source")
	}
	for _, entry := range entries {
		p := path.Join(fs.AbsoluteDir(root), entry.Name())
		if !entry.IsDir() {
			*files = append(*files, shared.NewFile(p))
			continue
		}
		dir := shared.NewDir(p)
		*dirs = append(*dirs, dir)
		// recurse into subdirectory
		err = Walk(logger, fs, dir, files, dirs)
		if err != nil {
			return fmt.Errorf("cannot collect files: %v", err)
		}
	}
	return nil
}

func CopyAndApplyTemplate(fs FileSystem, f shared.File, destination shared.File, obj any) error {
	// Read the file from the embedded file system
	data, err := fs.ReadFile(f)
	if err != nil {
		return fmt.Errorf("could not read file: %v", err)
	}
	out, err := ApplyTemplate(string(data), obj)
	if err != nil {
		return fmt.Errorf("cannot apply template: %v", err)
	}
	file, err := os.OpenFile(fs.AbsoluteFile(destination), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("failed opening file %s: %s", destination, err)
	}
	_, err = file.Write([]byte(out))
	if err != nil {
		return fmt.Errorf("failed writing to file %s: %s", destination, err)
	}
	err = file.Close()
	if err != nil {
		return fmt.Errorf("failed closing file %s: %s", destination, err)
	}
	return nil
}

func CopyAndApply(logger shared.BaseLogger, fs FileSystem, root shared.Dir, destination shared.Dir, obj any) error {
	logger.Debugf("applying template to directory %s -> %s", root, destination)
	err := shared.CheckDirectoryOrCreate(fs.AbsoluteDir(destination))
	if err != nil {
		return logger.Wrapf(err, "cannot check or create directory")
	}
	var dirs []shared.Dir
	var files []shared.File
	err = Walk(logger, fs, root, &files, &dirs)
	if err != nil {
		return fmt.Errorf("cannot read template directory: %v", err)
	}
	logger.Debugf("walked %d directories and %d files", len(dirs), len(files))
	for _, d := range dirs {
		// We take the relative path from the root directory
		rel, err := d.RelativeFrom(root)
		if err != nil {
			return logger.Wrapf(err, "cannot get relative path")
		}
		dest := destination.Join(*rel)
		err = shared.CheckDirectoryOrCreate(dest.Absolute())
		if err != nil {
			return logger.Wrapf(err, "cannot check or create directory for destination")
		}

	}
	for _, f := range files {

		rel, err := f.RelativeFrom(root)
		if err != nil {
			return logger.Wrapf(err, "cannot get relative path")
		}

		target := path.Join(fs.AbsoluteDir(destination), rel.Relative())

		d, found := strings.CutSuffix(target, ".tmpl")
		if !found {
			return logger.Errorf("cannot cut suffix from %s", d)
		}
		err = CopyAndApplyTemplate(fs, f, shared.NewFile(d), obj)
		logger.Debugf("copied template %s to %s", f, destination)
		if err != nil {
			return fmt.Errorf("cannot copy template: %v", err)
		}
	}
	return nil
}
