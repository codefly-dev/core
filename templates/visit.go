package templates

import (
	"context"
	"fmt"
	"path"

	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

type FileVisitor interface {
	Apply(ctx context.Context, from shared.File, to shared.Dir) error
	Skip(file string) bool
}

type Ignore interface {
	Ignore(file shared.File) bool
}

func CopyAndVisit(ctx context.Context, fs shared.FileSystem, root shared.Dir, destination shared.Dir, visitor FileVisitor) error {
	w := wool.Get(ctx).In("templates.CopyAndVisit")
	_, err := shared.CheckDirectoryOrCreate(ctx, fs.AbsoluteDir(destination))
	if err != nil {
		return w.Wrapf(err, "cannot check or create directory")
	}
	var dirs []shared.Dir
	var files []shared.File
	err = Walk(ctx, fs, root, visitor, &files, &dirs)
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
		// Hack
		if visitor.Skip(d.Absolute()) {
			continue
		}
		_, err = shared.CheckDirectoryOrCreate(ctx, dest.Absolute())
		if err != nil {
			return w.Wrapf(err, "cannot check or create directory for destination")
		}

	}
	for _, f := range files {
		if visitor.Skip(f.Base()) {
			w.Trace("ignoring", wool.FileField(f.Base()))
			continue
		}

		rel, err := f.RelativeFrom(root)
		if err != nil {
			return w.Wrapf(err, "cannot get relative path")
		}

		target := path.Join(fs.AbsoluteDir(destination), rel.RelativePath())
		err = visitor.Apply(ctx, f, shared.NewDir(target))
		if err != nil {
			return w.Wrapf(err, "cannot apply visitor")
		}
	}
	return nil
}
