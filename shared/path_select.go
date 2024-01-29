package shared

import (
	"context"
	"regexp"
	"strings"
)

type PathSelect interface {
	Keep(p string) bool
}

type ContextPathSelectKey string

const (
	PathSelectKey ContextPathSelectKey = "path-select"
)

func GetPathSelect(ctx context.Context) PathSelect {
	if sel, ok := ctx.Value(PathSelectKey).(PathSelect); ok {
		return sel
	}
	return IgnoreNone()
}

/*

Implementations

*/

func IgnoreNone() PathSelect {
	return &IgnoreNoneHandler{}
}

type IgnoreNoneHandler struct{}

func (i *IgnoreNoneHandler) Keep(string) bool {
	return true
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

func (ign *IgnorePatterns) Keep(file string) bool {
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

func (ign *SelectPatterns) Keep(file string) bool {
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
