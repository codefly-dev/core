package templates

import (
	"fmt"
	"github.com/codefly-dev/core/shared"
	"path"
)

type FileVisitor interface {
	Apply(path shared.File, to shared.Dir) error
	Ignore(file shared.File) bool
}

func CopyAndVisit(logger shared.BaseLogger, fs FileSystem, root shared.Dir, destination shared.Dir, visitor FileVisitor) error {
	logger.Tracef("visiting to directory %s -> %s", root, destination)
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
		// Hack
		if visitor.Ignore(shared.NewFile(d.Absolute())) {
			continue
		}
		err = shared.CheckDirectoryOrCreate(dest.Absolute())
		if err != nil {
			return logger.Wrapf(err, "cannot check or create directory for destination")
		}

	}
	for _, f := range files {

		if visitor.Ignore(f) {
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
