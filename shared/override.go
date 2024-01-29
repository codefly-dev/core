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

// Implementations

func OverrideException(sel PathSelect) *OverrideExceptionHandler {
	return &OverrideExceptionHandler{
		PathSelect: sel,
	}
}

// OverrideExceptionHandler replaces all paths EXCEPT the ones selected
type OverrideExceptionHandler struct {
	PathSelect
}

var _ Override = &OverrideExceptionHandler{}

func (handler *OverrideExceptionHandler) Replace(p string) bool {
	if handler.PathSelect == nil {
		return true
	}
	return !handler.PathSelect.Keep(p)
}

// OverrideAll overrides all paths
func OverrideAll() Override {
	return &OverrideAllHandler{}
}

type OverrideAllHandler struct{}

var _ Override = &OverrideAllHandler{}

func (*OverrideAllHandler) Replace(string) bool {
	return true
}

// SkipAll skips all paths
func SkipAll() *SkipAllHandler {
	return &SkipAllHandler{}
}

type SkipAllHandler struct{}

var _ Override = &SkipAllHandler{}

func (*SkipAllHandler) Replace(string) bool {
	return false
}
