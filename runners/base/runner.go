package base

import (
	"context"
	"io"

	"github.com/codefly-dev/core/resources"
)

/*
A RunnerEnvironment controls running processes.
Implementations:
- local
- docker
- kubernetes (future)
*/
type RunnerEnvironment interface {
	// Init setup the environment
	Init(ctx context.Context) error

	// NewProcess creates a new process for the environment
	NewProcess(bin string, args ...string) (Proc, error)

	// Stop the environment: can potentially be restarted
	Stop(ctx context.Context) error

	// Shutdown the environment: stop and remove all resources
	Shutdown(ctx context.Context) error

	// WithBinary ensures a binary is visible in the environment
	WithBinary(bin string) error

	// WithEnvironmentVariables sets the environment variables
	WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable)
}

// Proc is a generic process interface
// Implementations:
// - LocalEnvironment process: obtained from a local environment
// - Docker process: obtained by running in a Docker environment
type Proc interface {
	Start(ctx context.Context) error

	// Run starts the process and waits for it. It returns nil only when the
	// process exited with status 0, or when this Proc's own Stop requested
	// the termination. A signal from anywhere else — a timeout, a parent
	// dying, a deploy's SIGTERM — is a failure and is returned: a process
	// that did not finish did not succeed. A cancelled ctx returns ctx.Err().
	Run(ctx context.Context) error
	Stop(ctx context.Context) error

	// Wait blocks until the process exits or ctx is cancelled. Returns the
	// process's exit error (nil for clean exit, *exec.ExitError for non-zero).
	// Must be safe to call after Start. Multiple callers may share the result
	// via an internal channel — implementations should make Wait idempotent.
	// Used by supervisors to detect when a fire-and-forget Start'd process
	// dies so they can propagate the failure (instead of leaking the parent).
	Wait(ctx context.Context) error

	IsRunning(ctx context.Context) (bool, error)

	// WaitOn For Run, optional, we can wait on another process
	WaitOn(s string)

	// WithDir overrides the location where the Proc runs
	WithDir(local string)

	// WithOutput output to send the logs
	WithOutput(w io.Writer)

	// WithEnvironmentVariables adds environment variables
	WithEnvironmentVariables(ctx context.Context, envs ...*resources.EnvironmentVariable)

	// WithEnvironmentVariablesAppend appends environment variables
	WithEnvironmentVariablesAppend(ctx context.Context, env *resources.EnvironmentVariable, sep string)

	// StdinPipe returns a writer connected to the process's stdin.
	// Must be called before Start/Run.
	StdinPipe() (io.WriteCloser, error)

	// StdoutPipe returns a reader connected to the process's stdout (raw bytes).
	// Must be called before Start/Run. When used, WithOutput is bypassed for
	// stdout; stderr still goes to the writer set via WithOutput.
	StdoutPipe() (io.ReadCloser, error)
}

// HostProcess is implemented by a Proc that runs as a process of this machine
// (native and nix; not a container's). It is how the owner of a detached
// server learns that server's birth from the handle it already holds, instead
// of asking the server for its PID.
type HostProcess interface {
	// Process returns the identity of the process Start launched, as recorded
	// at its start: the PID together with the boot and start identity that
	// tell it from a later process reusing the number. It is the process the
	// Proc started; a server that forks away from it is not that process. It
	// wraps ErrProcessNotFound before Start. The identity stays the recorded
	// birth after the process ends: re-inspect before signalling
	// (SignalProcess).
	Process() (ProcessIdentity, error)
}
