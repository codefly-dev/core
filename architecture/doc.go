// Package architecture computes the dependency graph across services
// and modules and produces a topological execution order.
//
// Given a workspace, it loads every module and service, resolves their
// declared dependencies (by service name, endpoint, or configuration
// reference), detects cycles, and exposes inventory queries plus a
// stable run-order used by `codefly run` and the agent orchestrator.
package architecture
