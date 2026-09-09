// Package architecture computes the dependency graph across services
// and modules and produces a topological execution order.
//
// Given a workspace, it loads every module and service, resolves their
// declared dependencies (by service name, endpoint, or configuration
// reference), detects cycles, and exposes inventory queries plus a
// stable run-order used by `codefly run` and the agent orchestrator.
//
// Dependencies carry a kind (see resources.DependencyKind), so a closure is
// computed per elementary stage: ForStage keeps only the edges that constrain
// that stage, and cycles are detected within a stage rather than across the
// union of all of them. OrderFor decomposes a caller-facing phase (test and
// deploy each build then run) into its ordered stages.
// See docs/dependency-kinds.md.
package architecture
