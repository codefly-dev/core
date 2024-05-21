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
	WithEnvironmentVariables(envs ...*resources.EnvironmentVariable)
}

// Proc is a generic process interface
// Implementations:
// - LocalEnvironment process: obtained from a local environment
// - Docker process: obtained by running in a Docker environment
type Proc interface {
	Start(ctx context.Context) error
	Run(ctx context.Context) error
	Stop(ctx context.Context) error

	IsRunning(ctx context.Context) (bool, error)

	// WaitOn For Run, optional, we can wait on another process
	WaitOn(s string)

	// WithDir overrides the location where the Proc runs
	WithDir(local string)

	// WithOutput output to send the logs
	WithOutput(w io.Writer)

	// WithEnvironmentVariables adds environment variables
	WithEnvironmentVariables(envs ...*resources.EnvironmentVariable)
}
