package sdk

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/codefly-dev/core/cli"
)

// WithDependencies starts all dependencies declared in the current service's
// service.codefly.yaml using the codefly CLI. This handles arbitrarily deep
// dependency graphs — the CLI resolves and starts everything in order.
//
// Connection strings are injected as environment variables (the standard
// codefly pattern). Use Connection() or os.Getenv() to retrieve them.
//
// Options:
//
//	sdk.WithDependencies(ctx)
//	sdk.WithDependencies(ctx, sdk.Debug())
//	sdk.WithDependencies(ctx, sdk.Timeout(30*time.Second))
func WithDependencies(ctx context.Context, opts ...DependencyOption) (*DependencyEnv, error) {
	var cliOpts []cli.OptionFunc
	for _, o := range opts {
		o(&cliOpts)
	}

	deps, err := cli.WithDependencies(ctx, cliOpts...)
	if err != nil {
		return nil, fmt.Errorf("start dependencies: %w", err)
	}

	return &DependencyEnv{deps: deps}, nil
}

// DependencyEnv wraps a running set of codefly-managed dependencies.
type DependencyEnv struct {
	deps *cli.Dependencies
}

// DependencyOption configures WithDependencies.
type DependencyOption func(*[]cli.OptionFunc)

// Debug enables debug logging.
func Debug() DependencyOption {
	return func(opts *[]cli.OptionFunc) {
		*opts = append(*opts, cli.WithDebug())
	}
}

// Connection returns a connection string from environment variables.
// It searches for the standard codefly env var pattern for the given
// service and endpoint name.
func (e *DependencyEnv) Connection(service, name string) string {
	// Codefly injects connection strings as env vars with a standard naming pattern.
	// Try common patterns.
	patterns := []string{
		fmt.Sprintf("CODEFLY__SERVICE_%s__%s__CONNECTION", normalize(service), normalize(name)),
		fmt.Sprintf("CODEFLY__%s__%s__CONNECTION", normalize(service), normalize(name)),
	}
	for _, p := range patterns {
		if v := os.Getenv(p); v != "" {
			return v
		}
	}
	return ""
}

// Stop tears down all running dependencies.
func (e *DependencyEnv) Stop(ctx context.Context) error {
	if e.deps != nil {
		return e.deps.Destroy(ctx)
	}
	return nil
}

func normalize(s string) string {
	s = strings.ToUpper(s)
	s = strings.ReplaceAll(s, "-", "_")
	s = strings.ReplaceAll(s, "/", "__")
	return s
}
