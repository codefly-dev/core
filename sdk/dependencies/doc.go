// Package dependencies manages Codefly dependency sessions through the CLI's
// public control API. It owns the launched CLI process group, session identity,
// readiness checks and scoped configuration carriers. Agent engines and
// container execution remain in the CLI process.
//
// Existing sdk.WithDependencies callers retain the same implementation through
// compatibility aliases. New consumers should import this package, avoiding
// sdk.Env's deprecated in-process agent-management dependencies.
package dependencies
