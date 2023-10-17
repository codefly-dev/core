package templates

import "github.com/codefly-dev/core/shared"

type FileVisitor interface {
	Apply(path shared.File, to shared.Dir) error
	Ignore(file shared.File) bool
}

/*


 */
//
//func CopyWithModifier(source Source, start shared.Directory, target shared.Directory, visitor FileVisitor, override shared.BaseLogger) error {
//	logger := shared.NewLogger("templates.CopyWithModifierOld").IfNot(override)
//	logger.Debugf("copying directory <%s>", start)
//	if source == nil {
//		logger.Debugf("source is nil")
//		return nil
//	}
//	entries, err := source.ReadDir(start)
//	if err != nil {
//		return logger.Wrapf(err, "cannot read directory")
//	}
//
//	for _, entry := range entries {
//		logger.Debugf("entry %s", entry)
//		t := path.Join(S(target), entry.Name())
//		if entry.IsDir() {
//			logger.Debugf("found directory: %s", entry)
//			dir := entry.Name()
//			// Create the directory
//			err = shared.CheckDirectoryOrCreate(t)
//			if err != nil {
//				return logger.Wrapf(err, "found non-empty directory, skipping")
//			}
//			err = CopyWithModifier(source.Next(), shared.Directory(path.Join(string(start), dir)), shared.Directory(t), visitor, override)
//			if err != nil {
//				return logger.Wrapf(err, "cannot copy directory")
//			}
//			continue
//		}
//		// Copy
//
//		if err != nil {
//			return logger.Wrapf(err, "cannot get relative path")
//		}
//
//		from := path.Join(string(start), entry.Name())
//		// logger.Debugf("Looking at file %s", from)
//		if visitor.Ignore(shared.File(from)) {
//			continue
//		}
//		err := visitor.Apply(shared.File(from), target)
//		if err != nil {
//			return logger.Wrapf(err, "cannot apply visitor")
//		}
//	}
//	return nil
//}
