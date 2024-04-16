package base

import (
	"context"
	"io"

	"github.com/codefly-dev/core/configurations"
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

	// Clear removes all resources
	Clear(ctx context.Context) error

	// NewProcess creates a new process for the environment
	NewProcess(bin string, args ...string) (Proc, error)

	// Stop the environment: can potentially be restarted
	Stop(ctx context.Context) error

	// Shutdown the environment: all resources will be deleted
	Shutdown(ctx context.Context) error

	// WithEnvironmentVariables sets the environment variables
	WithEnvironmentVariables(envs ...configurations.EnvironmentVariable)
}

type Proc interface {
	Start(ctx context.Context) error
	Run(ctx context.Context) error
	Stop(ctx context.Context) error

	WithOutput(w io.Writer)
	WithEnvironmentVariables(envs ...configurations.EnvironmentVariable)
}

type Runner interface {

	// Setting parameters

	WithDir(dir string)
	WithEnvironmentVariables(envs ...configurations.EnvironmentVariable)
	WithBin(bin string)
	WithArguments(args ...string)
	WithOutput(w io.Writer)

	// Interface

	Init(ctx context.Context) error

	Run(ctx context.Context) error
	Start(ctx context.Context) error

	Stop() error
}
