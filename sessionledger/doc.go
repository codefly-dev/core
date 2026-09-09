// Package sessionledger records what one Codefly invocation owns, so a later
// invocation can clean up exactly what a crashed one created — and nothing
// else.
//
// # Why a ledger
//
// Codefly already tracks one kind of ownership durably: runners/base persists
// an authenticated record per spawned process group so a SIGKILLed CLI's
// orphans can be reaped on the next run. That record answers "which process
// groups did some codefly leave behind", but not "what did THIS invocation
// create, which of it holds data, and what is a later invocation allowed to
// delete". Docker containers answered the second question with an owner PID
// label and a name heuristic, which is not ownership: a recycled PID makes a
// dead owner look alive, and a name collision makes someone else's container
// look like ours.
//
// The ledger is the missing record. One session file per invocation lists every
// resource that invocation touched, whether it created or borrowed it, whether
// it holds data, the validated identity that proves the backing resource is
// still the same one, and the outcome of every cleanup attempt.
//
// # Lifecycle policy
//
// A [Lifecycle] is applied to a whole session and decides an [Action] per
// resource:
//
//   - [LifecycleStop] stops execution and retains data. This is the default
//     end-of-run and the crash-recovery path.
//   - [LifecycleKeepRunning] is explicit reuse: nothing is stopped or deleted,
//     and the session stays warm for a later [Store.Acquire].
//   - [LifecycleReset] deletes owned disposable state. It is refused outright
//     when the session holds borrowed data, rather than partially applied.
//
// [Borrowed] resources are never stopped and never deleted under any
// lifecycle. A nested session that borrows its parent's database cannot take
// that database down when it ends.
//
// # Crash recovery
//
// Resources are declared before they are created ([Session.Declare]) and
// confirmed after ([Session.Commit]), so a crash between the two still leaves a
// record naming what may exist. Recovery never trusts that record on its own:
// [Recover] asks the resource's [Backend] to Claim it, and the backend must
// prove — from the backend's own state, not from the ledger — that the resource
// exists and belongs to that invocation. A resource that is gone, or that
// exists but cannot be proven ours, is preserved and reported. Nothing is ever
// found by name sweep or killed by PID alone.
//
// # Warm reuse
//
// A [ModeReusable] session outlives its process. [Store.Acquire] reattaches to
// it only when the caller presents the same semantic fingerprint; a mismatch
// returns an [IncompatibleReuseError] naming what changed and leaves the warm
// state untouched. Acquire is serialized per key by an advisory file lock, so
// concurrent attach and reset cannot interleave.
//
// # Secrets
//
// The record has no free-form text field. Every persisted string is validated
// against a bounded pattern — slugs, namespaced kinds, hex digests, executable
// base names — so connection strings, tokens and argv cannot be written into
// the ledger even by a caller that tries.
package sessionledger
