package resources

import "context"

type workspaceMigrationRefusalKey struct{}

// WithWorkspaceMigrationRefusal prevents legacy flat-workspace migration during
// this load and every recursively imported workspace load. A legacy module file
// is refused before it is decoded, merged, saved or removed. Ordinary loads keep
// their migration behavior. Hosts remain responsible for the effects of any
// resolver they attach to the context.
func WithWorkspaceMigrationRefusal(ctx context.Context) context.Context {
	return context.WithValue(ctx, workspaceMigrationRefusalKey{}, true)
}
