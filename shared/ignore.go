package shared

import (
	"context"
	"regexp"
	"strings"
)

type Ignore interface {
	Skip(p string) bool
}

type ContextIgnoreKey string

const (
	IgnoreKey ContextIgnoreKey = "ignore"
)

func WithIgnore(ctx context.Context, ignore Ignore) context.Context {
	return context.WithValue(ctx, IgnoreKey, ignore)
}

func GetIgnore(ctx context.Context) Ignore {
	if ignore, ok := ctx.Value(IgnoreKey).(Ignore); ok {
		return ignore
	}
	return IgnoreNone()
}

/*

Implementations

*/

func IgnoreNone() Ignore {
	return &IgnoreNoneHandler{}
}

type IgnoreNoneHandler struct{}

func (i *IgnoreNoneHandler) Skip(string) bool {
	return false
}

func sanitizePattern(pattern string) string {
	return "^" + strings.ReplaceAll(pattern, "*", ".*") + "$"
}

type IgnorePatterns struct {
	patterns []string
	regex    []regexp.Regexp
}

func NewIgnore(patterns ...string) *IgnorePatterns {
	ign := &IgnorePatterns{patterns: patterns}
	for _, p := range patterns {
		ign.regex = append(ign.regex, *regexp.MustCompile(sanitizePattern(p)))
	}
	return ign
}

func (ign *IgnorePatterns) Skip(file string) bool {
	for _, pattern := range ign.patterns {
		if strings.Contains(file, pattern) {
			return true
		}
	}
	for i := range ign.regex {
		reg := &ign.regex[i]
		if reg.MatchString(file) {
			return true
		}
	}
	return false
}

type SelectPatterns struct {
	patterns []string
	regex    []regexp.Regexp
}

func NewSelect(patterns ...string) *SelectPatterns {
	ign := &SelectPatterns{patterns: patterns}
	for _, p := range patterns {
		ign.regex = append(ign.regex, *regexp.MustCompile(sanitizePattern(p)))
	}
	return ign
}

func (ign *SelectPatterns) Skip(file string) bool {
	for _, pattern := range ign.patterns {
		if strings.Contains(file, pattern) {
			return false
		}
	}
	for i := range ign.regex {
		reg := &ign.regex[i]
		if reg.MatchString(file) {
			return false
		}
	}
	return true
}
