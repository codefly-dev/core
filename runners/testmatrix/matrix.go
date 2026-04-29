// Package testmatrix provides a multi-environment test harness. A plugin
// lifecycle test wrapped in ForEachEnvironment runs once per available
// execution backend (native, docker, nix), with t.Run sub-tests so
// failures are attributed to a specific mode.
//
// This is the guardrail for the "every plugin tests all three execution
// environments" architectural rule: plugin authors write one test, and
// parity across modes becomes a CI-enforced guarantee rather than a
// claim.
package testmatrix

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	runners "github.com/codefly-dev/core/runners/base"
)

// EnvFactory is a constructor that returns a RunnerEnvironment bound to
// dir. Each backend provides one. Returning (nil, nil) is a programmer
// error — backends MUST be available when the matrix runs. If a backend
// can't be available on every dev/CI box, exclude it via Only(...) at
// the call site instead.
type EnvFactory func(ctx context.Context, dir string) (runners.RunnerEnvironment, error)

// Case wraps a (name, factory) pair for a single backend.
type Case struct {
	Name    string
	Factory EnvFactory
}

// Option tweaks the set of backends used by ForEachEnvironment.
type Option func(*options)

type options struct {
	dockerImage *resources.DockerImage
	onlyNames   map[string]bool
}

// WithDockerImage overrides the Docker image for the `docker` case. Most
// plugins want their language's canonical image (golang:1.26, python:3.12,
// node:22-alpine) rather than the default alpine:3.20 which has no
// toolchains beyond /bin/sh.
func WithDockerImage(img *resources.DockerImage) Option {
	return func(o *options) { o.dockerImage = img }
}

// Only restricts the matrix to the named backends (e.g. "native", "nix").
// Useful when a plugin genuinely cannot support one — prefer Skip-in-factory
// over Only when the backend is merely unavailable on the host.
func Only(names ...string) Option {
	return func(o *options) {
		o.onlyNames = make(map[string]bool, len(names))
		for _, n := range names {
			o.onlyNames[n] = true
		}
	}
}

// ForEachEnvironment runs fn against each supported backend as a sub-test.
// A backend that isn't available on the host (Docker not running, nix
// not installed, missing flake.nix) FAILS the sub-test loudly — silent
// skips hide drift. To exclude an irrelevant backend at the call site,
// use Only("native", "nix"). To exclude all infra-dependent backends
// from a build, set the `skip_infra` build tag at the test-file level.
//
// `dir` is the workspace or source directory the plugin expects to
// operate on. For native/nix it's used as cwd; for docker it's bind-mounted.
func ForEachEnvironment(t *testing.T, dir string, fn func(t *testing.T, env runners.RunnerEnvironment), opts ...Option) {
	t.Helper()
	cfg := &options{}
	for _, opt := range opts {
		opt(cfg)
	}
	for _, c := range casesFor(cfg) {
		t.Run(c.Name, func(t *testing.T) {
			ctx := context.Background()
			env, err := c.Factory(ctx, dir)
			if err != nil {
				t.Fatalf("%s: cannot create env: %v", c.Name, err)
			}
			if env == nil {
				t.Fatalf("%s: backend not available on this host — install/start it, restrict the matrix with Only(...), or run with -tags skip_infra", c.Name)
			}
			t.Cleanup(func() {
				_ = env.Shutdown(context.Background())
			})
			if err := env.Init(ctx); err != nil {
				t.Fatalf("%s: Init: %v", c.Name, err)
			}
			fn(t, env)
		})
	}
}

func casesFor(cfg *options) []Case {
	all := defaultCases()
	if cfg.dockerImage != nil {
		// Replace the docker case's factory so the caller's image is used.
		img := cfg.dockerImage
		for i := range all {
			if all[i].Name == "docker" {
				all[i].Factory = func(ctx context.Context, dir string) (runners.RunnerEnvironment, error) {
					if !runners.DockerEngineRunning(ctx) {
						return nil, nil
					}
					uniq := filepath.Base(dir) + "-testmatrix"
					return runners.NewDockerEnvironment(ctx, img, dir, uniq)
				}
			}
		}
	}
	if cfg.onlyNames == nil {
		return all
	}
	filtered := make([]Case, 0, len(all))
	for _, c := range all {
		if cfg.onlyNames[c.Name] {
			filtered = append(filtered, c)
		}
	}
	return filtered
}

// defaultCases returns the three standard backends. Each factory returns
// (nil, nil) when the backend isn't usable on the current host so the
// test harness can Skip instead of Fail.
func defaultCases() []Case {
	return []Case{
		{
			Name: "native",
			Factory: func(ctx context.Context, dir string) (runners.RunnerEnvironment, error) {
				return runners.NewNativeEnvironment(ctx, dir)
			},
		},
		{
			Name: "nix",
			Factory: func(ctx context.Context, dir string) (runners.RunnerEnvironment, error) {
				if !runners.CheckNixInstalled() || !runners.IsNixSupported() {
					return nil, nil
				}
				if _, err := os.Stat(filepath.Join(dir, "flake.nix")); err != nil {
					return nil, nil // no flake → skip
				}
				return runners.NewNixEnvironment(ctx, dir)
			},
		},
		{
			Name: "docker",
			Factory: func(ctx context.Context, dir string) (runners.RunnerEnvironment, error) {
				if !runners.DockerEngineRunning(ctx) {
					return nil, nil
				}
				// A Docker test needs an image. Default to alpine — cheap,
				// universally available, has /bin/sh. Callers that need a
				// richer image should use a custom Case in their package.
				img := &resources.DockerImage{Name: "alpine", Tag: "3.20"}
				uniq := filepath.Base(dir) + "-testmatrix"
				return runners.NewDockerEnvironment(ctx, img, dir, uniq)
			},
		},
	}
}
