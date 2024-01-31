package templates

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
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

func Walk(ctx context.Context, fs shared.FileSystem, root string, pathSelect shared.PathSelect, files *[]string, dirs *[]string) error {
	w := wool.Get(ctx).In("templates.Walk")
	entries, err := fs.ReadDir(root)
	if err != nil {
		return w.Wrapf(err, "cannot got to target source")
	}
	for _, entry := range entries {
		if !pathSelect.Keep(entry.Name()) {
			continue
		}
		p := path.Join(root, entry.Name())
		if !entry.IsDir() {
			*files = append(*files, p)
			continue
		}
		*dirs = append(*dirs, p)
		// recurse into subdirectory
		err = Walk(ctx, fs, p, pathSelect, files, dirs)
		if err != nil {
			return w.Wrapf(err, "cannot walk into subdirectory")
		}
	}
	return nil
}

type AlreadyExistError struct {
	file string
}

func (a AlreadyExistError) Error() string {
	return fmt.Sprintf("file %s already exists", a.file)
}

func Copy(ctx context.Context, fs shared.FileSystem, f string, destination string) error {
	w := wool.Get(ctx).In("templates.Copy", wool.Field("from", f), wool.Field("to", destination))
	// Read the file from the embedded file system
	data, err := fs.ReadFile(f)
	if err != nil {
		return w.Wrap(err)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return w.Wrap(err)
	}
	_, err = file.Write([]byte(data))
	if err != nil {
		return w.Wrap(err)
	}
	err = file.Close()
	if err != nil {
		return w.Wrap(err)
	}
	return nil
}

func CopyAndApplyTemplate(ctx context.Context, fs shared.FileSystem, f string, destination string, obj any) error {
	w := wool.Get(ctx).In("templates.CopyAndApplyTemplate", wool.Field("from", f), wool.Field("to", destination))
	// Read the file from the embedded file system
	data, err := fs.ReadFile(f)
	if err != nil {
		return w.Wrap(err)
	}
	out, err := ApplyTemplate(string(data), obj)
	if err != nil {
		return w.Wrapf(err, destination)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return w.Wrap(err)
	}
	_, err = file.Write([]byte(out))
	if err != nil {
		return w.Wrap(err)
	}
	err = file.Close()
	if err != nil {
		return w.Wrap(err)
	}
	return nil
}

func ApplyTemplateFrom(ctx context.Context, fs shared.FileSystem, f string, obj any) (string, error) {
	w := wool.Get(ctx).In("templates.ApplyTemplateFrom", wool.Field("from", f))
	// Read the file from the embedded file system
	f = fmt.Sprintf("%s.tmpl", f)
	data, err := fs.ReadFile(f)
	if err != nil {
		return "", fmt.Errorf("could not read file: %v", err)
	}
	res, err := ApplyTemplate(string(data), obj)
	if err != nil {
		return "", w.Wrap(err)
	}
	return res, nil
}

type Replacer interface {
	Do([]byte) ([]byte, error)
}

func CopyAndReplace(ctx context.Context, fs shared.FileSystem, f string, destination string, replacer Replacer) error {
	w := wool.Get(ctx).In("templates.CopyAndReplace", wool.Field("from", f), wool.Field("to", destination))
	// Read the file from the embedded file system
	data, err := fs.ReadFile(f)
	if err != nil {
		return w.Wrap(err)
	}
	out, err := replacer.Do(data)
	if err != nil {
		return w.Wrap(err)
	}
	file, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return w.Wrap(err)
	}
	_, err = file.Write([]byte(out))
	if err != nil {
		return w.Wrap(err)
	}
	err = file.Close()
	if err != nil {
		return w.Wrap(err)
	}
	return nil
}

type Templator struct {
	PathSelect shared.PathSelect
	Override   shared.Override
}

var _ shared.PathSelect = &Templator{}
var _ shared.Override = &Templator{}

func (t *Templator) Keep(name string) bool {
	if t.PathSelect == nil {
		return true

	}
	return t.PathSelect.Keep(name)
}

func (t *Templator) Replace(p string) bool {
	if t.Override == nil {
		return true
	}
	return t.Override.Replace(p)
}

func CopyAndApply(ctx context.Context, fs shared.FileSystem, root string, destination string, obj any) error {
	t := Templator{}
	return t.CopyAndApply(ctx, fs, root, destination, obj)
}

func (t *Templator) CopyAndApply(ctx context.Context, fs shared.FileSystem, root string, destination string, obj any) error {
	w := wool.Get(ctx).In("templates.CopyAndApply")

	_, err := shared.CheckDirectoryOrCreate(ctx, destination)

	if err != nil {
		return w.Wrapf(err, "cannot check or create directory")
	}
	var dirs []string
	var files []string
	err = Walk(ctx, fs, root, t, &files, &dirs)
	if err != nil {
		return fmt.Errorf("cannot read template directory: %v", err)
	}
	w.Trace(fmt.Sprintf("walked %d directories and %d files", len(dirs), len(files)))
	for _, d := range dirs {
		// We take the relative path from the root directory
		rel, err := filepath.Rel(root, d)
		if err != nil {
			return w.Wrapf(err, "cannot get relative path")
		}
		dest := filepath.Join(destination, rel)
		_, err = shared.CheckDirectoryOrCreate(ctx, dest)
		if err != nil {
			return w.Wrapf(err, "cannot check or create directory for destination")
		}

	}
	for _, f := range files {
		rel, err := filepath.Rel(root, f)
		if err != nil {
			return w.Wrapf(err, "cannot get relative path")
		}

		target := path.Join(destination, rel)

		d, found := strings.CutSuffix(target, ".tmpl")
		if !found {
			err = Copy(ctx, fs, f, target)
			if err != nil {
				return fmt.Errorf("cannot copy file: %v", err)
			}
			continue
		}

		if shared.FileExists(d) && !t.Replace(d) {
			w.Trace("file %s already exists: skipping", wool.FileField(d))
			continue
		}
		err = CopyAndApplyTemplate(ctx, fs, f, d, obj)
		w.Trace(fmt.Sprintf("copied template %s to %s", f, destination))
		if err != nil {
			return w.Wrapf(err, "cannot copy template")
		}
	}
	return nil
}

type FileVisitor interface {
	shared.PathSelect
	Apply(ctx context.Context, from string, to string) error
}

func CopyAndVisit(ctx context.Context, fs shared.FileSystem, root string, destination string, visitor FileVisitor) error {
	w := wool.Get(ctx).In("templates.CopyAndVisit")
	_, err := shared.CheckDirectoryOrCreate(ctx, destination)
	if err != nil {
		return w.Wrapf(err, "cannot check or create directory")
	}
	var dirs []string
	var files []string
	err = Walk(ctx, fs, root, visitor, &files, &dirs)
	if err != nil {
		return fmt.Errorf("cannot read template directory: %v", err)
	}
	w.Trace(fmt.Sprintf("walked %d directories and %d files", len(dirs), len(files)))
	for _, d := range dirs {
		// We take the relative path from the root directory
		rel, err := filepath.Rel(root, d)
		if err != nil {
			return w.Wrapf(err, "cannot get relative path")
		}
		dest := filepath.Join(destination, rel)
		if !visitor.Keep(dest) {
			continue
		}
		_, err = shared.CheckDirectoryOrCreate(ctx, dest)
		if err != nil {
			return w.Wrapf(err, "cannot check or create directory for destination")
		}

	}
	for _, f := range files {
		base := filepath.Base(f)
		if !visitor.Keep(base) {
			w.Trace("ignoring", wool.FileField(base))
			continue
		}

		rel, err := filepath.Rel(root, f)
		if err != nil {
			return w.Wrapf(err, "cannot get relative path")
		}

		target := path.Join(destination, rel)
		err = visitor.Apply(ctx, f, target)
		if err != nil {
			return w.Wrapf(err, "cannot apply visitor")
		}
	}
	return nil
}
