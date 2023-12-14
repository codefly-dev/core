package shared

import (
	"context"
)

type Override interface {
	Replace(p string) bool
}

type ContextOverrideKey string

const (
	OverrideKey ContextOverrideKey = "override"
)

func WithOverride(ctx context.Context, override Override) context.Context {
	return context.WithValue(ctx, OverrideKey, override)
}

func GetOverride(ctx context.Context) Override {
	if override, ok := ctx.Value(OverrideKey).(Override); ok {
		return override
	}
	return SilentOverride()
}

/*

Implementations

*/

func SilentOverride() Override {
	return &SilentOverrideHandler{}
}

type SilentOverrideHandler struct{}

func (s SilentOverrideHandler) Replace(string) bool {
	return true
}

func Skip() Override {
	return &SkipOverrideHandler{}
}

type SkipOverrideHandler struct{}

func (s SkipOverrideHandler) Replace(string) bool {
	return false
}
