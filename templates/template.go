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

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

func ApplyTemplate(t string, data any) (string, error) {
	funcMap := template.FuncMap{
		"default": func(defaultValue, value interface{}) interface{} {
			if value == nil {
				return defaultValue
			}
			if s, ok := value.(string); ok && s == "" {
				return defaultValue
			}
			return value
		},
		"toYaml": func(v interface{}) string {
			data, err := yaml.Marshal(v)
			if err != nil {
				return ""
			}
			return string(data)
		},
		"indent": func(spaces int, v string) string {
			pad := strings.Repeat(" ", spaces)
			return pad + strings.Replace(v, "\n", "\n"+pad, -1)
		},
		"quote": func(str string) string {
			return fmt.Sprintf("%q", str)
		},
		"upper":      strings.ToUpper,
		"lower":      strings.ToLower,
		"trim":       strings.TrimSpace,
		"trimPrefix": strings.TrimPrefix,
		"trimSuffix": strings.TrimSuffix,
		"ternary": func(condition bool, trueVal, falseVal interface{}) interface{} {
			if condition {
				return trueVal
			}
			return falseVal
		},
		"contains": strings.Contains,
		"split":    strings.Split,
		"join":     strings.Join,
		"dict": func(values ...interface{}) (map[string]interface{}, error) {
			if len(values)%2 != 0 {
				return nil, fmt.Errorf("invalid dict call")
			}
			dict := make(map[string]interface{}, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, fmt.Errorf("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
		"merge": func(dst map[string]interface{}, src ...map[string]interface{}) map[string]interface{} {
			for _, s := range src {
				for k, v := range s {
					dst[k] = v
				}
			}
			return dst
		},
	}

	tmpl, err := template.New("template").Funcs(funcMap).Parse(t)
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

type NameReplacer interface {
	NewName(base string) string
}

type Templator struct {
	PathSelect shared.PathSelect
	Override   shared.Override
	NameReplacer
}

type CutTemplateSuffix struct {
}

func (CutTemplateSuffix) NewName(base string) string {
	if cut, ok := strings.CutSuffix(base, ".tmpl"); ok {
		return cut
	}
	return base
}

type NoOpName struct {
}

func (NoOpName) NewName(base string) string {
	return base
}

type AddTemplateSuffix struct {
}

func (AddTemplateSuffix) NewName(base string) string {
	return fmt.Sprintf("%s.tmpl", base)
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

func (t *Templator) NewName(base string) string {
	if t.NameReplacer == nil {
		return base
	}
	return t.NameReplacer.NewName(base)
}

func CopyAndApply(ctx context.Context, fs shared.FileSystem, root string, destination string, obj any) error {
	t := Templator{NameReplacer: CutTemplateSuffix{}}
	return t.CopyAndApply(ctx, fs, root, destination, obj)
}

type TemplateVisitor struct {
	fs      shared.FileSystem
	tmp     any
	ignores []string
}

func (t TemplateVisitor) Ignore(ctx context.Context, file string) bool {
	for _, ignore := range t.ignores {
		if strings.Contains(file, ignore) {
			return true
		}
	}
	return false
}

func (t TemplateVisitor) Apply(ctx context.Context, from string, to string) error {
	if strings.HasSuffix(from, ".tmpl") {
		return CopyAndApplyTemplate(ctx, t.fs, from, to, t.tmp)
	}
	return Copy(ctx, t.fs, from, to)
}

func (t *Templator) CopyAndApply(ctx context.Context, fs shared.FileSystem, root string, destination string, obj any) error {
	visitor := TemplateVisitor{tmp: obj, fs: fs}
	return t.WalkAndVisit(ctx, fs, root, destination, visitor)
}

type FileVisitor interface {
	Ignore(ctx context.Context, file string) bool
	Apply(ctx context.Context, from string, to string) error
}

func CopyAndVisit(ctx context.Context, fs shared.FileSystem, root string, destination string, nameReplacer NameReplacer, visitor FileVisitor) error {
	t := Templator{NameReplacer: nameReplacer}
	return t.WalkAndVisit(ctx, fs, root, destination, visitor)
}

func (t *Templator) WalkAndVisit(ctx context.Context, fs shared.FileSystem, root string, destinationDir string, visitor FileVisitor) error {
	w := wool.Get(ctx).In("templates.CopyAndApply")

	_, err := shared.CheckDirectoryOrCreate(ctx, destinationDir)
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
		dest := filepath.Join(destinationDir, rel)
		_, err = shared.CheckDirectoryOrCreate(ctx, dest)
		if err != nil {
			return w.Wrapf(err, "cannot check or create directory for destinationDir")
		}

	}
	for _, f := range files {
		base, err := filepath.Rel(root, f)
		if err != nil {
			return w.Wrapf(err, "cannot get relative path")
		}

		if visitor.Ignore(ctx, base) {
			continue
		}
		// New name
		base = t.NewName(base)
		target := path.Join(destinationDir, base)

		exists, err := shared.FileExists(ctx, target)
		if err != nil {
			return w.Wrapf(err, "cannot check if file exists")
		}
		if exists && !t.Replace(target) {
			w.Trace("file %s already exists: skipping", wool.FileField(target))
			continue
		}
		err = visitor.Apply(ctx, f, target)
		w.Trace(fmt.Sprintf("copied template %s to %s", f, destinationDir))
		if err != nil {
			return w.Wrapf(err, "cannot copy template")
		}
	}
	return nil
}
