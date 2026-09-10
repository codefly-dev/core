// Package executionplan is the immutable, versioned record of what one Codefly
// invocation resolved to: the selected service closure, the backend and artifact
// chosen for each node, the typed edges between them, where every configuration
// value will come from, and the ordered schema prerequisites.
//
// The model is pure. It holds no filesystem paths outside Invocation, no
// configuration values and no secrets, and it never loads anything: callers such
// as architecture.Closure build a plan from resolved resources, and this package
// canonicalizes, validates and fingerprints it.
package executionplan
