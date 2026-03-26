package golang

import (
	"context"
	"path"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/languages"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

// SetGoRuntimeContext determines the runtime context (native, nix, container)
// based on the requested context and available Go toolchain.
// Returns the resolved RuntimeContext — callers assign it to their own struct.
func SetGoRuntimeContext(runtimeContext *basev0.RuntimeContext) *basev0.RuntimeContext {
	if runtimeContext.Kind == resources.RuntimeContextNix {
		return resources.NewRuntimeContextNix()
	}
	if runtimeContext.Kind == resources.RuntimeContextFree || runtimeContext.Kind == resources.RuntimeContextNative {
		if languages.HasGoRuntime(nil) {
			return resources.NewRuntimeContextNative()
		}
	}
	return resources.NewRuntimeContextContainer()
}

// RunnerConfig holds the parameters needed to create a Go runner environment.
type RunnerConfig struct {
	RuntimeImage   *resources.DockerImage
	WorkspacePath  string
	RelativeSource string
	UniqueName     string
	CacheLocation  string
	Settings       *GoAgentSettings
}

// CreateRunner creates a GoRunnerEnvironment based on the runtime context.
// For container runtimes, the caller is responsible for port bindings (agent-specific).
func CreateRunner(ctx context.Context, runtimeCtx *basev0.RuntimeContext, cfg RunnerConfig) (*GoRunnerEnvironment, error) {
	w := wool.Get(ctx).In("golang.CreateRunner")

	sourceRelative := path.Join(cfg.RelativeSource, cfg.Settings.GoSourceDir())

	var env *GoRunnerEnvironment
	var err error

	switch runtimeCtx.Kind {
	case resources.RuntimeContextContainer:
		env, err = NewDockerGoRunner(ctx, cfg.RuntimeImage, cfg.WorkspacePath, sourceRelative, cfg.UniqueName)
		if err != nil {
			return nil, w.Wrapf(err, "cannot create docker runner")
		}
		// Mount service.codefly.yaml into the container root.
		env.WithFile(path.Join(cfg.WorkspacePath, cfg.RelativeSource, "service.codefly.yaml"), "/service.codefly.yaml")
	case resources.RuntimeContextNix:
		env, err = NewNixGoRunner(ctx, cfg.WorkspacePath, sourceRelative)
		if err != nil {
			return nil, w.Wrapf(err, "cannot create nix runner")
		}
	default:
		env, err = NewNativeGoRunner(ctx, cfg.WorkspacePath, sourceRelative)
		if err != nil {
			return nil, w.Wrapf(err, "cannot create local runner")
		}
	}

	env.WithLocalCacheDir(cfg.CacheLocation)
	env.WithDebugSymbol(cfg.Settings.DebugSymbols)
	env.WithRaceConditionDetection(cfg.Settings.RaceConditionDetectionRun)
	env.WithCGO(cfg.Settings.WithCGO)
	env.WithWorkspace(cfg.Settings.WithWorkspace)

	return env, nil
}

// RunGoTests runs `go test -json -cover ./...` and returns parsed results.
func RunGoTests(ctx context.Context, env *GoRunnerEnvironment, sourceLocation string, envVars []*resources.EnvironmentVariable) (*TestSummary, error) {
	_ = env.Env().WithBinary("codefly")

	proc, err := env.Env().NewProcess("go", "test", "-json", "-cover", "./...")
	if err != nil {
		return nil, err
	}

	var capture LineCapture
	proc.WithOutput(&capture)
	proc.WithDir(sourceLocation)
	proc.WithEnvironmentVariables(ctx, envVars...)

	runErr := proc.Run(ctx)
	summary := ParseTestJSON(capture.String())

	if runErr != nil {
		return summary, runErr
	}
	return summary, nil
}

// DestroyGoRuntime cleans up cache and shuts down container runtime if applicable.
func DestroyGoRuntime(ctx context.Context, runtimeCtx *basev0.RuntimeContext, runtimeImage *resources.DockerImage, cacheLocation, workspacePath, relativeSource, uniqueName string) error {
	_ = shared.EmptyDir(ctx, cacheLocation)
	if runtimeCtx.Kind == resources.RuntimeContextContainer {
		dockerEnv, err := NewDockerGoRunner(ctx, runtimeImage, workspacePath, relativeSource, uniqueName)
		if err != nil {
			return err
		}
		return dockerEnv.Shutdown(ctx)
	}
	return nil
}
