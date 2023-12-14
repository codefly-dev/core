package shared

import "context"

// NewContext provides a context with a logger
func NewContext() context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, Base, NewLogger())
	return ctx
}

// ChildContext
func ChildContext(ctx context.Context, name string) context.Context {
	logger := GetLogger(ctx)
	return context.WithValue(ctx, Base, logger.With(name))

}
