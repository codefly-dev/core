package rust

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

// SetRustRuntimeContext determines the runtime context (native, nix,
// container) based on the requested context and the available Cargo
// toolchain. Mirrors golang.SetGoRuntimeContext.
func SetRustRuntimeContext(runtimeContext *basev0.RuntimeContext) *basev0.RuntimeContext {
	if runtimeContext.Kind == resources.RuntimeContextNix {
		return resources.NewRuntimeContextNix()
	}
	if runtimeContext.Kind == resources.RuntimeContextFree || runtimeContext.Kind == resources.RuntimeContextNative {
		if languages.HasCargoRuntime(nil) {
			return resources.NewRuntimeContextNative()
		}
	}
	return resources.NewRuntimeContextContainer()
}

// RunnerConfig holds the parameters needed to create a Rust runner environment.
type RunnerConfig struct {
	RuntimeImage   *resources.DockerImage
	WorkspacePath  string
	RelativeSource string
	UniqueName     string
	CacheLocation  string
	Settings       *RustAgentSettings
}

// CreateRunner creates a RustRunnerEnvironment based on the runtime context.
// For container runtimes, the caller is responsible for port bindings.
func CreateRunner(ctx context.Context, runtimeCtx *basev0.RuntimeContext, cfg RunnerConfig) (*RustRunnerEnvironment, error) {
	w := wool.Get(ctx).In("rust.CreateRunner")

	sourceRelative := path.Join(cfg.RelativeSource, cfg.Settings.RustSourceDir())

	var env *RustRunnerEnvironment
	var err error

	switch runtimeCtx.Kind {
	case resources.RuntimeContextContainer:
		env, err = NewDockerRustRunner(ctx, cfg.RuntimeImage, cfg.WorkspacePath, sourceRelative, cfg.UniqueName)
		if err != nil {
			return nil, w.Wrapf(err, "cannot create docker runner")
		}
		env.WithFile(path.Join(cfg.WorkspacePath, cfg.RelativeSource, "service.codefly.yaml"), "/service.codefly.yaml")
	case resources.RuntimeContextNix:
		env, err = NewNixRustRunner(ctx, cfg.WorkspacePath, sourceRelative)
		if err != nil {
			return nil, w.Wrapf(err, "cannot create nix runner")
		}
	default:
		env, err = NewNativeRustRunner(ctx, cfg.WorkspacePath, sourceRelative)
		if err != nil {
			return nil, w.Wrapf(err, "cannot create local runner")
		}
	}

	env.WithLocalCacheDir(cfg.CacheLocation)
	env.WithDebugSymbol(cfg.Settings.DebugSymbols)
	env.WithRelease(cfg.Settings.Release)

	return env, nil
}

// TestOptions controls how `cargo test` is invoked.
type TestOptions struct {
	// Target is an optional libtest name filter (substring). Empty runs all.
	Target  string
	Verbose bool
	Release bool
	Timeout string // unused by cargo today; reserved for parity

	// Coverage is accepted for parity. Cargo has no built-in coverage; it is
	// a no-op (external tools like cargo-llvm-cov / tarpaulin handle it).
	Coverage bool

	// Filters are libtest name patterns. libtest ORs multiple filters, so they
	// are passed verbatim after `--`.
	Filters []string

	// Features are Cargo feature flags (`--features f1,f2`).
	Features []string

	// ExtraArgs are appended verbatim to the libtest side of the command line.
	ExtraArgs []string

	// OnEvent, when non-nil, is invoked for every parsed test result line as
	// it is written to stdout — enables real-time progress streaming.
	OnEvent func(TestEvent)
}

// RunCargoTests runs `cargo test` (with `--no-fail-fast` so every test binary
// runs) and returns parsed results. The raw stdout is persisted to
// <cacheDir>/last-test.txt for post-mortem, mirroring RunGoTests.
func RunCargoTests(ctx context.Context, env *RustRunnerEnvironment, sourceLocation string, envVars []*resources.EnvironmentVariable, opts ...TestOptions) (*TestSummary, error) {
	_ = env.Env().WithBinary("codefly")

	var opt TestOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	args := []string{"test", "--no-fail-fast", "--color", "never"}
	if opt.Release {
		args = append(args, "--release")
	}
	if len(opt.Features) > 0 {
		args = append(args, "--features", joinFeatures(opt.Features))
	}

	// Everything after `--` goes to the libtest harness.
	libtest := []string{}
	if opt.Verbose {
		libtest = append(libtest, "--nocapture")
	}
	if opt.Target != "" {
		libtest = append(libtest, opt.Target)
	}
	libtest = append(libtest, opt.Filters...)
	libtest = append(libtest, opt.ExtraArgs...)
	if len(libtest) > 0 {
		args = append(args, "--")
		args = append(args, libtest...)
	}

	proc, err := env.Env().NewProcess("cargo", args...)
	if err != nil {
		return nil, err
	}

	var capture *LineCapture
	if opt.OnEvent != nil {
		streaming := &StreamingTestWriter{OnEvent: opt.OnEvent}
		proc.WithOutput(streaming)
		capture = &streaming.LineCapture
	} else {
		capture = &LineCapture{}
		proc.WithOutput(capture)
	}
	proc.WithDir(cargoTestWorkDir(sourceLocation))
	proc.WithEnvironmentVariables(ctx, envVars...)

	runErr := proc.Run(ctx)
	rawOutput := capture.String()
	summary := ParseCargoTest(rawOutput)

	if cacheDir := env.LocalCacheDir(ctx); cacheDir != "" {
		if err := writeLastTestOutput(cacheDir, rawOutput); err != nil {
			wool.Get(ctx).In("RunCargoTests").
				Debug("could not persist last-test.txt (non-fatal)", wool.ErrField(err))
		}
	}

	if runErr != nil {
		return summary, runErr
	}
	return summary, nil
}

// cargoTestWorkDir walks up from sourceLocation to the directory holding
// Cargo.toml so cargo resolves the right workspace.
func cargoTestWorkDir(sourceLocation string) string {
	cur := sourceLocation
	for {
		if _, err := os.Stat(filepath.Join(cur, "Cargo.toml")); err == nil {
			return cur
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return sourceLocation
		}
		cur = parent
	}
}

// writeLastTestOutput dumps the raw `cargo test` stream to
// <cacheDir>/last-test.txt. Atomic via tmp + rename.
func writeLastTestOutput(cacheDir, raw string) error {
	if err := os.MkdirAll(cacheDir, 0o755); err != nil {
		return err
	}
	p := filepath.Join(cacheDir, "last-test.txt")
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(raw), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// BuildOptions controls how `cargo build` is invoked.
type BuildOptions struct {
	Release  bool
	Features []string
}

// RunCargoBuild runs `cargo build` and returns combined output.
func RunCargoBuild(ctx context.Context, env *RustRunnerEnvironment, sourceLocation string, envVars []*resources.EnvironmentVariable, opts ...BuildOptions) (string, error) {
	args := []string{"build"}
	if len(opts) > 0 {
		if opts[0].Release {
			args = append(args, "--release")
		}
		if len(opts[0].Features) > 0 {
			args = append(args, "--features", joinFeatures(opts[0].Features))
		}
	}

	proc, err := env.Env().NewProcess("cargo", args...)
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

// LintOptions controls how `cargo clippy` is invoked.
type LintOptions struct {
	// AllTargets runs clippy over tests/examples/benches too.
	AllTargets bool
}

// RunCargoLint runs `cargo clippy` and returns combined output.
func RunCargoLint(ctx context.Context, env *RustRunnerEnvironment, sourceLocation string, envVars []*resources.EnvironmentVariable, opts ...LintOptions) (string, error) {
	args := []string{"clippy"}
	if len(opts) > 0 && opts[0].AllTargets {
		args = append(args, "--all-targets")
	}

	proc, err := env.Env().NewProcess("cargo", args...)
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

// joinFeatures joins Cargo feature flags into the comma-separated form
// `--features` expects.
func joinFeatures(features []string) string {
	return strings.Join(features, ",")
}

// DestroyRustRuntime cleans up cache and shuts down the container runtime if
// applicable. Mirrors golang.DestroyGoRuntime.
func DestroyRustRuntime(ctx context.Context, runtimeCtx *basev0.RuntimeContext, runtimeImage *resources.DockerImage, cacheLocation, workspacePath, relativeSource, uniqueName string) error {
	_ = shared.EmptyDir(ctx, cacheLocation)
	if runtimeCtx.Kind == resources.RuntimeContextContainer {
		dockerEnv, err := NewDockerRustRunner(ctx, runtimeImage, workspacePath, relativeSource, uniqueName)
		if err != nil {
			return err
		}
		return dockerEnv.Shutdown(ctx)
	}
	return nil
}
