package code

import (
	"context"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// FixInput is the complete, in-memory source snapshot passed to a language
// plugin. Fixers must not write the workspace themselves; DefaultCodeServer
// commits the returned content through its VFS after the whole pipeline
// succeeds.
type FixInput struct {
	Path    string
	Content []byte
	Mode    basev0.FixMode
}

// FixResult is the language-aware rewrite and its evidence.
type FixResult struct {
	Content []byte
	Actions []string
	Output  string
}

// SourceFixer formats and safely rewrites one source snapshot. Implementations
// may invoke project-native tools, but they must return content rather than
// mutating the workspace so edits remain single-write VFS operations.
type SourceFixer func(context.Context, FixInput) (FixResult, error)

const maxFixOutputBytes = 64 * 1024

func boundedFixOutput(output string) string {
	if len(output) <= maxFixOutputBytes {
		return output
	}
	half := maxFixOutputBytes / 2
	return output[:half] + "\n... codefly truncated fixer output ...\n" + output[len(output)-half:]
}
