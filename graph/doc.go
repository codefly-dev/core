// Package graph implements the directed-graph primitives that
// architecture and other dependency-aware packages use.
//
// It exposes a generic Graph[T] with topological sort and cycle
// detection — the building blocks for resolving service and module
// dependencies into a stable execution order.
package graph
