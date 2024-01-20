package shared

import "context"

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
	return OverrideAll()
}

/*

Implementations

*/

func OverrideException(ignore Ignore) *OverrideExceptionHandler {
	return &OverrideExceptionHandler{
		Ignore: ignore,
	}
}

type OverrideExceptionHandler struct {
	Ignore
}

var _ Override = &OverrideExceptionHandler{}

func (o *OverrideExceptionHandler) Replace(p string) bool {
	if o.Ignore != nil && o.Skip(p) {
		return false
	}
	return true
}

// Override
func OverrideAll() Override {
	return &OverrideAllHandler{}
}

type OverrideAllHandler struct{}

var _ Override = &OverrideAllHandler{}

func (o *OverrideAllHandler) Replace(string) bool {
	return true
}

// SkipAll
func SkipAll() *SkipAllHandler {
	return &SkipAllHandler{}
}

type SkipAllHandler struct{}

var _ Override = &SkipAllHandler{}

func (o *SkipAllHandler) Replace(string) bool {
	return false
}
