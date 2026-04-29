// Package tui provides the bubbletea-based terminal UI components used
// by `codefly run` and other interactive CLI commands.
//
// It includes the run dashboard (per-service state, logs, dependency
// view), the headless renderer used in CI / piped output, and the
// shared widgets (spinners, progress bars, log tail) that are reused
// across commands.
package tui
