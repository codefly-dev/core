package templates

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path"
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

func Walk(ctx context.Context, fs shared.FileSystem, root shared.Dir, ignore shared.Ignore, files *[]shared.File, dirs *[]shared.Dir) error {
	w := wool.Get(ctx).In("templates.Walk")
	entries, err := fs.ReadDir(root)
	if err != nil {
		return w.Wrapf(err, "cannot got to target source")
	}
	for _, entry := range entries {
		if ignore.Skip(entry.Name()) {
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
		err = Walk(ctx, fs, dir, ignore, files, dirs)
		if err != nil {
			return w.Wrapf(err, "cannot walk into subdirectory")
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

func Copy(ctx context.Context, fs shared.FileSystem, f shared.File, destination shared.File) error {
	w := wool.Get(ctx).In("templates.Copy", wool.Field("from", f), wool.Field("to", destination))
	// Read the file from the embedded file system
	data, err := fs.ReadFile(f)
	if err != nil {
		return w.Wrap(err)
	}
	file, err := os.OpenFile(fs.AbsoluteFile(destination), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
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

func CopyAndApplyTemplate(ctx context.Context, fs shared.FileSystem, f shared.File, destination shared.File, obj any) error {
	w := wool.Get(ctx).In("templates.CopyAndApplyTemplate", wool.Field("from", f), wool.Field("to", destination))
	// Read the file from the embedded file system
	data, err := fs.ReadFile(f)
	if err != nil {
		return w.Wrap(err)
	}
	out, err := ApplyTemplate(string(data), obj)
	if err != nil {
		return w.Wrap(err)
	}
	file, err := os.OpenFile(fs.AbsoluteFile(destination), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
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

func CopyAndReplace(ctx context.Context, fs shared.FileSystem, f shared.File, destination shared.File, replacer Replacer) error {
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
	file, err := os.OpenFile(fs.AbsoluteFile(destination), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
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

func CopyAndApply(ctx context.Context, fs shared.FileSystem, root shared.Dir, destination shared.Dir, obj any) error {
	w := wool.Get(ctx).In("templates.CopyAndApply")
	w.Info("copying and applying template")

	_, err := shared.CheckDirectoryOrCreate(ctx, fs.AbsoluteDir(destination))
	override := shared.GetOverride(ctx)
	ignore := shared.GetIgnore(ctx)

	if err != nil {
		return w.Wrapf(err, "cannot check or create directory")
	}
	var dirs []shared.Dir
	var files []shared.File
	err = Walk(ctx, fs, root, ignore, &files, &dirs)
	if err != nil {
		return fmt.Errorf("cannot read template directory: %v", err)
	}
	w.Trace(fmt.Sprintf("walked %d directories and %d files", len(dirs), len(files)))
	for _, d := range dirs {
		// We take the relative path from the root directory
		rel, err := d.RelativeFrom(root)
		if err != nil {
			return w.Wrapf(err, "cannot get relative path")
		}
		dest := destination.Join(*rel)
		_, err = shared.CheckDirectoryOrCreate(ctx, dest.Absolute())
		if err != nil {
			return w.Wrapf(err, "cannot check or create directory for destination")
		}

	}
	for _, f := range files {
		rel, err := f.RelativeFrom(root)
		if err != nil {
			return w.Wrapf(err, "cannot get relative path")
		}

		target := path.Join(fs.AbsoluteDir(destination), rel.Relative())

		d, found := strings.CutSuffix(target, ".tmpl")
		if !found {
			err = Copy(ctx, fs, f, shared.NewFile(target))
			if err != nil {
				return fmt.Errorf("cannot copy file: %v", err)
			}
			continue
		}

		if shared.FileExists(d) && !override.Replace(d) {
			w.Trace("file %s already exists: skipping", wool.FileField(d))
			continue
		}
		err = CopyAndApplyTemplate(ctx, fs, f, shared.NewFile(d), obj)
		w.Trace(fmt.Sprintf("copied template %s to %s", f, destination))
		if err != nil {
			return w.Wrapf(err, "cannot copy template")
		}
	}
	return nil
}
