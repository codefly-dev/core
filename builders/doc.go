// Package builders provides file-level dependency tracking for
// incremental agent builds.
//
// Dependency declarations enumerate files (and globs) that an artifact
// depends on; UpdateCache snapshots their mtimes and Updated reports
// whether anything changed since the last snapshot. This lets agents
// skip rebuilds when nothing relevant has changed — the same idea as
// make, scoped per-agent and persisted to the local cache directory.
package builders
