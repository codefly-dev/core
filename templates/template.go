package templates

import (
	"bytes"
	"fmt"
	"os"
	"path"
	"strings"
	"text/template"

	"github.com/codefly-dev/core/shared"
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

func Walk(logger shared.BaseLogger, fs shared.FileSystem, root shared.Dir, ignore Ignore, files *[]shared.File, dirs *[]shared.Dir) error {
	entries, err := fs.ReadDir(root)
	if err != nil {
		return logger.Wrapf(err, "cannot got to target source")
	}
	for _, entry := range entries {
		if ignore != nil && ignore.Ignore(shared.NewFile(entry.Name())) {
			continue
		}
		p := path.Join(fs.AbsoluteDir(root), entry.Name())
		if !entry.IsDir() {
			*files = append(*files, shared.NewFile(p))
			continue
		}
		dir := shared.NewDir(p)
		*dirs = append(*dirs, dir)
		// recurse into subdirectory
		err = Walk(logger, fs, dir, ignore, files, dirs)
		if err != nil {
			return fmt.Errorf("cannot collect files: %v", err)
		}
	}
	return nil
}

type AlreadyExistError struct {
	file shared.File
}

func (a AlreadyExistError) Error() string {
	return fmt.Sprintf("file %s already exists", a.file)
}

func Copy(fs shared.FileSystem, f shared.File, destination shared.File) error {
	// Read the file from the embedded file system
	data, err := fs.ReadFile(f)
	if err != nil {
		return fmt.Errorf("could not read file: %v", err)
	}
	file, err := os.OpenFile(fs.AbsoluteFile(destination), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("failed opening file %s: %s", destination, err)
	}
	_, err = file.Write([]byte(data))
	if err != nil {
		return fmt.Errorf("failed writing to file %s: %s", destination, err)
	}
	err = file.Close()
	if err != nil {
		return fmt.Errorf("failed closing file %s: %s", destination, err)
	}
	return nil
}

func CopyAndApplyTemplate(fs shared.FileSystem, f shared.File, destination shared.File, obj any) error {
	// Read the file from the embedded file system
	data, err := fs.ReadFile(f)
	if err != nil {
		return fmt.Errorf("could not read file: %v", err)
	}
	out, err := ApplyTemplate(string(data), obj)
	if err != nil {
		return fmt.Errorf("cannot apply template in %v: %v", f.Base(), err)
	}
	file, err := os.OpenFile(fs.AbsoluteFile(destination), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
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

func ApplyTemplateFrom(fs shared.FileSystem, f string, obj any) (string, error) {
	// Read the file from the embedded file system
	f = fmt.Sprintf("%s.tmpl", f)
	data, err := fs.ReadFile(shared.NewFile(f))
	if err != nil {
		return "", fmt.Errorf("could not read file: %v", err)
	}
	return ApplyTemplate(string(data), obj)
}

type Replacer interface {
	Do([]byte) ([]byte, error)
}

func CopyAndReplace(fs shared.FileSystem, f shared.File, destination shared.File, replacer Replacer) error {
	// Read the file from the embedded file system
	data, err := fs.ReadFile(f)
	if err != nil {
		return fmt.Errorf("could not read file: %v", err)
	}
	out, err := replacer.Do(data)
	if err != nil {
		return fmt.Errorf("replacer failed: %v", err)
	}
	file, err := os.OpenFile(fs.AbsoluteFile(destination), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
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

type NoOpIgnore struct{}

var _ Ignore = NoOpIgnore{}

func (ign NoOpIgnore) Ignore(file shared.File) bool {
	return false
}

type IgnorePatterns struct {
	patterns []string
}

var _ Ignore = IgnorePatterns{}

func NewIgnore(patterns ...string) IgnorePatterns {
	return IgnorePatterns{patterns: patterns}
}

func (ign IgnorePatterns) Ignore(file shared.File) bool {
	for _, pattern := range ign.patterns {
		if strings.Contains(file.Base(), pattern) {
			return true
		}
	}
	return false
}

type Override interface {
	Override(string) bool
}

type TemplateOption struct {
	Destination string
	Ignore
	Override
}

type TemplateOptionFunc func(*TemplateOption)

func WithIgnore(ignore Ignore) TemplateOptionFunc {
	return func(opt *TemplateOption) {
		opt.Ignore = ignore
	}
}

type NoIgnore struct {
}

func (i *NoIgnore) Ignore(file shared.File) bool {
	return false
}

func WithOverridePolicy(override Override) TemplateOptionFunc {
	return func(opt *TemplateOption) {
		opt.Override = override
	}
}

func WithOverrideAll() TemplateOptionFunc {
	return func(opt *TemplateOption) {
		opt.Override = &OverrideAll{}
	}
}

type OverrideAll struct{}

func (o *OverrideAll) Override(string) bool {
	return true
}

type SkipOnExist struct{}

func (o *SkipOnExist) Override(string) bool {
	return false
}

func Option(relativePath string, opts ...TemplateOptionFunc) *TemplateOption {
	opt := &TemplateOption{
		Destination: relativePath,
		Ignore:      &NoIgnore{},
		Override:    &SkipOnExist{},
	}
	for _, o := range opts {
		o(opt)
	}
	return opt
}

func CopyAndApply(logger shared.BaseLogger, fs shared.FileSystem, root shared.Dir, destination shared.Dir, obj any, opts ...TemplateOptionFunc) error {
	logger.Debugf("applying template to directory %s -> %s", root, destination)
	opt := Option(destination.Absolute(), opts...)
	err := shared.CheckDirectoryOrCreate(fs.AbsoluteDir(destination))
	if err != nil {
		return logger.Wrapf(err, "cannot check or create directory")
	}
	var dirs []shared.Dir
	var files []shared.File
	err = Walk(logger, fs, root, opt.Ignore, &files, &dirs)
	if err != nil {
		return fmt.Errorf("cannot read template directory: %v", err)
	}
	logger.DebugMe("walked %d directories and %d files", len(dirs), len(files))
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
			err = Copy(fs, f, shared.NewFile(target))
			if err != nil {
				return fmt.Errorf("cannot copy file: %v", err)
			}
		}

		if shared.FileExists(d) && !opt.Override.Override(d) {
			logger.Debugf("file %s already exists: skipping", d)
			continue
		}
		err = CopyAndApplyTemplate(fs, f, shared.NewFile(d), obj)
		logger.Debugf("copied template %s to %s", f, destination)
		if err != nil {
			return fmt.Errorf("cannot copy template: %v", err)
		}
	}
	return nil
}
