// Command cli-toolbox is the standalone binary form of the reusable single-CLI
// toolbox. It wraps one binary and runs it through a RunnerEnvironment so the
// binary is provisioned by Nix/Docker/Native rather than installed globally.
//
// Configuration:
//
//	CODEFLY_TOOLBOX_BINARY    — the CLI to wrap (e.g. "terraform"). Required.
//	CODEFLY_TOOLBOX_ENV       — "nix" | "native" (default). Nix runs the binary
//	                            under the workspace flake's devShell.
//	CODEFLY_TOOLBOX_WORKSPACE — working directory / flake dir (default cwd).
//	CODEFLY_TOOLBOX_VERSION   — version surfaced in Identity (default "0.0.0-dev").
package main

import (
	"context"
	"fmt"
	"os"

	"github.com/codefly-dev/core/agents"
	runners "github.com/codefly-dev/core/runners/base"
	"github.com/codefly-dev/core/toolbox/cli"
)

func main() {
	bin := os.Getenv("CODEFLY_TOOLBOX_BINARY")
	if bin == "" {
		fmt.Fprintln(os.Stderr, "cli-toolbox: CODEFLY_TOOLBOX_BINARY is required")
		os.Exit(1)
	}
	version := envOrDefault("CODEFLY_TOOLBOX_VERSION", "0.0.0-dev")
	workspace := os.Getenv("CODEFLY_TOOLBOX_WORKSPACE")
	if workspace == "" {
		if cwd, err := os.Getwd(); err == nil {
			workspace = cwd
		}
	}

	ctx := context.Background()
	env, err := buildEnv(ctx, os.Getenv("CODEFLY_TOOLBOX_ENV"), workspace)
	if err != nil {
		fmt.Fprintf(os.Stderr, "cli-toolbox: build environment: %v\n", err)
		os.Exit(1)
	}
	if err := env.Init(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "cli-toolbox: init environment: %v\n", err)
		os.Exit(1)
	}

	agents.ServeToolbox(cli.New(env, bin, version))
}

func buildEnv(ctx context.Context, kind, workspace string) (runners.RunnerEnvironment, error) {
	if kind == "nix" {
		return runners.NewNixEnvironment(ctx, workspace)
	}
	return runners.NewNativeEnvironment(ctx, workspace)
}

func envOrDefault(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
