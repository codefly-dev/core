package shared

import (
	"context"
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

func (i IgnoreNoneHandler) Skip(string) bool {
	return false
}

var _ Ignore = IgnoreNoneHandler{}

type IgnorePatterns struct {
	patterns []string
}

var _ Ignore = IgnorePatterns{}

func NewIgnore(patterns ...string) IgnorePatterns {
	return IgnorePatterns{patterns: patterns}
}

func (ign IgnorePatterns) Skip(file string) bool {
	for _, pattern := range ign.patterns {
		if strings.Contains(file, pattern) {
			return true
		}
	}
	return false
}
