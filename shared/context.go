package shared

import "context"

// NewContext provides a context with a logger
func NewContext() context.Context {
	ctx := context.Background()
	ctx = context.WithValue(ctx, Base, NewLogger("codefly"))
	return ctx
}
