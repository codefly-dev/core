// Package architecture computes the dependency graph across services
// and modules and produces a topological execution order.
//
// Given a workspace, it loads every module and service, resolves their
// declared dependencies (by service name, endpoint, or configuration
// reference), detects cycles, and exposes inventory queries plus a
// stable run-order used by `codefly run` and the agent orchestrator.
//
// Dependencies carry a kind (see resources.DependencyKind), so a closure is
// computed per execution phase: ForPhase keeps only the edges that constrain
// that phase, and cycles are detected within a phase rather than across the
// union of all of them. See docs/dependency-kinds.md.
package architecture
