package templates

import (
	"context"
	"fmt"
	"path"

	"github.com/codefly-dev/core/shared"
)

type FileVisitor interface {
	Apply(path shared.File, to shared.Dir) error
	Skip(file string) bool
}

type Ignore interface {
	Ignore(file shared.File) bool
}

func CopyAndVisit(ctx context.Context, fs shared.FileSystem, root shared.Dir, destination shared.Dir, visitor FileVisitor) error {
	logger := shared.GetBaseLogger(ctx).With("visiting to directory %s -> %s", root, destination)
	err := shared.CheckDirectoryOrCreate(ctx, fs.AbsoluteDir(destination))
	if err != nil {
		return logger.Wrapf(err, "cannot check or create directory")
	}
	var dirs []shared.Dir
	var files []shared.File
	err = Walk(logger, fs, root, visitor, &files, &dirs)
	if err != nil {
		return fmt.Errorf("cannot read template directory: %v", err)
	}
	logger.Tracef("walked %d directories and %d files", len(dirs), len(files))
	for _, d := range dirs {
		// We take the relative path from the root directory
		rel, err := d.RelativeFrom(root)
		if err != nil {
			return logger.Wrapf(err, "cannot get relative path")
		}
		dest := destination.Join(*rel)
		// Hack
		if visitor.Skip(d.Absolute()) {
			continue
		}
		err = shared.CheckDirectoryOrCreate(ctx, dest.Absolute())
		if err != nil {
			return logger.Wrapf(err, "cannot check or create directory for destination")
		}

	}
	for _, f := range files {
		if visitor.Skip(f.Base()) {
			logger.Tracef("ignoring %s", f)
			continue
		}

		rel, err := f.RelativeFrom(root)
		if err != nil {
			return logger.Wrapf(err, "cannot get relative path")
		}

		target := path.Join(fs.AbsoluteDir(destination), rel.RelativePath())
		err = visitor.Apply(f, shared.NewDir(target))
		if err != nil {
			return logger.Wrapf(err, "cannot apply visitor")
		}
	}
	return nil
}
