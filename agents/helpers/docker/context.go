package docker

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/moby/patternmatcher"
	"github.com/moby/patternmatcher/ignorefile"
)

type PreparedBuildContext struct {
	Root       string
	Dockerfile string
	directory  string
}

func (c *PreparedBuildContext) Close() error { return os.RemoveAll(c.directory) }

// PrepareBuildContext separates the build definition from COPY inputs and
// applies each ignore policy before any files reach BuildKit. Custom policy
// may exclude more files, but its negations cannot undo root exclusions.
func PrepareBuildContext(ctx context.Context, root, dockerfile, customIgnore string) (_ *PreparedBuildContext, err error) {
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	var matchers []*patternmatcher.PatternMatcher
	seen := map[string]bool{}
	for _, name := range []string{".dockerignore", dockerfile + ".dockerignore", customIgnore} {
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		file, openErr := os.Open(filepath.Join(root, name))
		if os.IsNotExist(openErr) {
			continue
		}
		if openErr != nil {
			return nil, openErr
		}
		patterns, readErr := ignorefile.ReadAll(file)
		closeErr := file.Close()
		if readErr != nil {
			return nil, readErr
		}
		if closeErr != nil {
			return nil, closeErr
		}
		matcher, matchErr := patternmatcher.New(patterns)
		if matchErr != nil {
			return nil, matchErr
		}
		matchers = append(matchers, matcher)
	}
	directory, err := os.MkdirTemp("", "codefly-build-context-")
	if err != nil {
		return nil, err
	}
	prepared := &PreparedBuildContext{Root: filepath.Join(directory, "context"), Dockerfile: filepath.Join(directory, "Dockerfile"), directory: directory}
	defer func() {
		if err != nil {
			_ = prepared.Close()
		}
	}()
	if err = os.Mkdir(prepared.Root, 0700); err != nil {
		return nil, err
	}
	if err = copyBuildFile(filepath.Join(root, dockerfile), prepared.Dockerfile); err != nil {
		return nil, err
	}
	err = filepath.WalkDir(root, func(file string, entry os.DirEntry, walkErr error) error {
		if file == directory {
			return filepath.SkipDir
		}
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, file)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		for _, matcher := range matchers {
			excluded, err := matcher.MatchesOrParentMatches(filepath.ToSlash(relative))
			if err != nil {
				return err
			}
			if excluded {
				return nil
			}
		}
		destination := filepath.Join(prepared.Root, relative)
		if err := os.MkdirAll(filepath.Dir(destination), 0755); err != nil {
			return err
		}
		if entry.IsDir() {
			return os.MkdirAll(destination, 0755)
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(file)
			if err != nil {
				return err
			}
			return os.Symlink(target, destination)
		}
		return copyBuildFile(file, destination)
	})
	if err != nil {
		return nil, err
	}
	return prepared, nil
}

func copyBuildFile(source, destination string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("unsupported build context file %s", source)
	}
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, info.Mode().Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(output, input)
	closeErr := output.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
