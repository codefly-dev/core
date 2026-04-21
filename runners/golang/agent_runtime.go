package golang

import (
	"context"
	"os"
	"path"
	"path/filepath"
	"strings"

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

// TestOptions controls how go test is invoked.
type TestOptions struct {
	// Target is a package path (e.g. "./handlers"), test function name
	// (e.g. "TestHealthEndpoint"), or pattern (e.g. "TestHealth.*").
	// Empty runs all tests ("./...").
	Target  string
	Verbose bool
	Race    bool
	Timeout string // e.g. "30s"

	// Coverage enables `-cover` instrumentation. Off by default because it
	// roughly doubles test-binary compile time; opt in per TestRequest.
	Coverage bool

	// OnEvent, when non-nil, is invoked for every `go test -json` event as
	// it is written to stdout. Enables real-time progress streaming to the
	// TUI / logger without waiting for RunGoTests to return. The full
	// summary is still built from the same underlying output after the
	// process exits.
	OnEvent func(TestEvent)
}

// RunGoTests runs `go test -json` with optional target/flags and returns
// parsed results. `-cover` is opt-in via TestOptions.Coverage.
//
// When env.LocalCacheDir(ctx) is non-empty, the full raw stdout from
// `go test -json` is persisted to <cacheDir>/last-test.json after the
// run regardless of pass/fail. This gives operators a debug surface
// richer than the TestSummary we return to the caller: failing tests
// can be re-parsed by hand, exit-2 collection errors are recoverable,
// and the exact set of events the agent saw is reproducible.
func RunGoTests(ctx context.Context, env *GoRunnerEnvironment, sourceLocation string, envVars []*resources.EnvironmentVariable, opts ...TestOptions) (*TestSummary, error) {
	_ = env.Env().WithBinary("codefly")

	args := []string{"test", "-json"}

	var opt TestOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	if opt.Verbose {
		args = append(args, "-v")
	}
	if opt.Race {
		args = append(args, "-race")
	}
	if opt.Timeout != "" {
		args = append(args, "-timeout", opt.Timeout)
	}
	if opt.Coverage {
		args = append(args, "-cover")
	}

	// Determine package target and optional -run filter.
	pkg := "./..."
	if opt.Target != "" {
		if isPackagePath(opt.Target) {
			// Target is a package path like "./handlers" or "./..."
			pkg = opt.Target
		} else {
			// Target is a test name or pattern — pass as -run filter
			args = append(args, "-run", opt.Target)
		}
	}
	args = append(args, pkg)

	proc, err := env.Env().NewProcess("go", args...)
	if err != nil {
		return nil, err
	}

	// Stream when a callback is provided; otherwise buffer only (the
	// original behavior). Both paths use LineCapture.String() at the end
	// to feed ParseTestJSON.
	var capture *LineCapture
	if opt.OnEvent != nil {
		streaming := &StreamingTestWriter{OnEvent: opt.OnEvent}
		proc.WithOutput(streaming)
		capture = &streaming.LineCapture
	} else {
		capture = &LineCapture{}
		proc.WithOutput(capture)
	}
	proc.WithDir(sourceLocation)
	proc.WithEnvironmentVariables(ctx, envVars...)

	runErr := proc.Run(ctx)
	rawOutput := capture.String()
	summary := ParseTestJSON(rawOutput)

	// Persist the raw JSON stream for post-mortem. Best-effort —
	// failure here should never mask a test result. Path is documented
	// so users / CI can read it directly after a `codefly test` run.
	if cacheDir := env.LocalCacheDir(ctx); cacheDir != "" {
		if err := writeLastTestOutput(cacheDir, rawOutput); err != nil {
			wool.Get(ctx).In("RunGoTests").
				Debug("could not persist last-test.json (non-fatal)",
					wool.ErrField(err))
		}
	}

	if runErr != nil {
		return summary, runErr
	}
	return summary, nil
}

// writeLastTestOutput dumps the raw `go test -json` stream to
// <cacheDir>/last-test.json. Atomic via tmp + rename.
func writeLastTestOutput(cacheDir, raw string) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(cacheDir, "last-test.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(raw), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// BuildOptions controls how go build is invoked.
type BuildOptions struct {
	// Target is a package path (e.g. "./handlers") or empty for "./...".
	Target string
}

// RunGoBuild runs `go build` with an optional target and returns combined output.
func RunGoBuild(ctx context.Context, env *GoRunnerEnvironment, sourceLocation string, envVars []*resources.EnvironmentVariable, opts ...BuildOptions) (string, error) {
	target := "./..."
	if len(opts) > 0 && opts[0].Target != "" {
		target = opts[0].Target
	}

	proc, err := env.Env().NewProcess("go", "build", target)
	if err != nil {
		return "", err
	}

	var capture LineCapture
	proc.WithOutput(&capture)
	proc.WithDir(sourceLocation)
	proc.WithEnvironmentVariables(ctx, envVars...)

	runErr := proc.Run(ctx)
	return capture.String(), runErr
}

// LintOptions controls how go vet is invoked.
type LintOptions struct {
	// Target is a package path (e.g. "./handlers") or empty for "./...".
	Target string
}

// RunGoLint runs `go vet` with an optional target and returns combined output.
func RunGoLint(ctx context.Context, env *GoRunnerEnvironment, sourceLocation string, envVars []*resources.EnvironmentVariable, opts ...LintOptions) (string, error) {
	target := "./..."
	if len(opts) > 0 && opts[0].Target != "" {
		target = opts[0].Target
	}

	proc, err := env.Env().NewProcess("go", "vet", target)
	if err != nil {
		return "", err
	}

	var capture LineCapture
	proc.WithOutput(&capture)
	proc.WithDir(sourceLocation)
	proc.WithEnvironmentVariables(ctx, envVars...)

	runErr := proc.Run(ctx)
	return capture.String(), runErr
}

// isPackagePath returns true if s looks like a Go package path rather than a test name/pattern.
func isPackagePath(s string) bool {
	if strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") {
		return true
	}
	// Contains a slash but doesn't start with uppercase (test names start uppercase)
	if strings.Contains(s, "/") {
		return true
	}
	return false
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
