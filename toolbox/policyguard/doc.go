// Package policyguard wraps a toolboxv0.ToolboxServer with policy
// decision point (PDP) enforcement. Every CallTool request hits the
// PDP first; a deny short-circuits with the PDP's reason as a tool-
// level error (NOT a transport error — the model should be able to
// see and reason about the refusal).
//
// Three-layer defense recap:
//
//  1. canonical registry — refuses commands routed to other toolboxes
//  2. policy guard (this package) — refuses tool calls per policy
//  3. OS sandbox — refuses syscalls outside the manifest's grant
//
// The PDP layer is BETWEEN the canonical registry (which decides
// "should this be in this toolbox?") and the OS sandbox (which
// decides "can this syscall happen?"). It's the layer that says
// "even though git owns this and even though the sandbox would
// allow it, the operator's policy doesn't allow this caller to
// invoke it right now."
package policyguard
