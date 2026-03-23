package base

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

/*
CompanionRunner is the golden wrapper for running companions.

It provides a unified interface that works across Docker, Nix, and local
runners. Consumers (LSP, code generation, etc.) program against this
interface and never care which backend is running underneath.

  - Docker: full isolation, mounts, port mapping
  - Nix: reproducible env via flake.nix, no isolation overhead
  - Local: direct host execution, zero overhead

Use NewCompanionRunner to create one -- it picks the best available backend.
*/
type CompanionRunner interface {
	// WithMount makes a host directory visible inside the companion.
	// Docker: bind mount. Nix/Local: no-op (already on host).
	WithMount(hostPath, targetPath string)

	// WithPortMapping maps a companion port to a host port.
	// Docker: Docker port mapping. Nix/Local: no-op (same network).
	WithPortMapping(ctx context.Context, hostPort, companionPort uint16)

	// WithWorkDir sets the working directory for all processes.
	WithWorkDir(dir string)

	// WithPause keeps the environment alive after Init.
	// Docker: runs `sleep infinity`. Nix/Local: no-op.
	WithPause()

	// Init starts the environment.
	Init(ctx context.Context) error

	// NewProcess creates a process inside the companion.
	NewProcess(bin string, args ...string) (Proc, error)

	// Shutdown stops and cleans up all resources.
	// Safe to call multiple times.
	Shutdown(ctx context.Context) error

	// RunnerEnv returns the underlying RunnerEnvironment for callers that need
	// WithEnvironmentVariables, WithBinary, etc. Same lifecycle as this companion.
	RunnerEnv() RunnerEnvironment

	// Backend returns which backend is in use (e.g. for path or port decisions).
	Backend() Backend
}

// Backend identifies which runner backend is in use.
type Backend string

const (
	BackendDocker Backend = "docker"
	BackendNix    Backend = "nix"
	BackendLocal  Backend = "local"
)

// CompanionOpts configures companion runner creation.
type CompanionOpts struct {
	// Name is a short name for this companion instance. When using the Docker
	// backend, the container name is prefixed with "codefly-" (e.g. codefly-lsp-go-123).
	Name string

	// SourceDir is the host directory to mount as /workspace (or work in).
	SourceDir string

	// Image is the Docker image (required for Docker backend, ignored otherwise).
	Image *resources.DockerImage

	// PreferredBackend forces a specific backend. If empty, auto-detect.
	PreferredBackend Backend
}

// NewCompanionRunner creates a companion runner using the best available backend.
//
// Selection order (unless PreferredBackend is set):
//  1. Docker -- if Docker is running and Image is provided
//  2. Nix    -- if flake.nix exists in SourceDir (future)
//  3. Local  -- always available as fallback
func NewCompanionRunner(ctx context.Context, opts CompanionOpts) (CompanionRunner, error) {
	w := wool.Get(ctx).In("NewCompanionRunner")

	backend := opts.PreferredBackend
	if backend == "" {
		backend = detectBackend(ctx, opts)
	}

	w.Info("selected companion backend", wool.Field("backend", string(backend)))

	switch backend {
	case BackendDocker:
		return newDockerCompanion(ctx, opts)
	case BackendNix:
		return newNixCompanion(ctx, opts)
	case BackendLocal:
		return newLocalCompanion(ctx, opts)
	default:
		return nil, fmt.Errorf("unknown backend: %s", backend)
	}
}

func detectBackend(ctx context.Context, opts CompanionOpts) Backend {
	// Docker: need a running engine and an image
	if opts.Image != nil && DockerEngineRunning(ctx) {
		return BackendDocker
	}
	// Nix: prefer when flake.nix exists in SourceDir and nix is installed
	if opts.SourceDir != "" && CheckNixInstalled() && IsNixSupported() {
		flakePath := filepath.Join(opts.SourceDir, "flake.nix")
		if _, err := os.Stat(flakePath); err == nil {
			return BackendNix
		}
	}
	return BackendLocal
}

// --- Docker companion adapter ---

type dockerCompanion struct {
	inner *DockerEnvironment
}

func newDockerCompanion(ctx context.Context, opts CompanionOpts) (*dockerCompanion, error) {
	if opts.Image == nil {
		return nil, fmt.Errorf("docker backend requires an image")
	}
	env, err := NewDockerEnvironment(ctx, opts.Image, opts.SourceDir, opts.Name)
	if err != nil {
		return nil, err
	}
	return &dockerCompanion{inner: env}, nil
}

func (d *dockerCompanion) WithMount(hostPath, targetPath string) {
	d.inner.WithMount(hostPath, targetPath)
}

func (d *dockerCompanion) WithPortMapping(ctx context.Context, hostPort, companionPort uint16) {
	d.inner.WithPortMapping(ctx, hostPort, companionPort)
}

func (d *dockerCompanion) WithWorkDir(dir string) {
	d.inner.WithWorkDir(dir)
}

func (d *dockerCompanion) WithPause() {
	d.inner.WithPause()
}

func (d *dockerCompanion) Init(ctx context.Context) error {
	return d.inner.Init(ctx)
}

func (d *dockerCompanion) NewProcess(bin string, args ...string) (Proc, error) {
	return d.inner.NewProcess(bin, args...)
}

func (d *dockerCompanion) Shutdown(ctx context.Context) error {
	return d.inner.Shutdown(ctx)
}

func (d *dockerCompanion) RunnerEnv() RunnerEnvironment {
	return d.inner
}

func (d *dockerCompanion) Backend() Backend { return BackendDocker }

// --- Local companion adapter ---

type localCompanion struct {
	inner   *NativeEnvironment
	workDir string
}

func newLocalCompanion(ctx context.Context, opts CompanionOpts) (*localCompanion, error) {
	env, err := NewNativeEnvironment(ctx, opts.SourceDir)
	if err != nil {
		return nil, err
	}
	return &localCompanion{inner: env, workDir: opts.SourceDir}, nil
}

// WithMount is a no-op for local runners -- host filesystem is already visible.
func (l *localCompanion) WithMount(_, _ string) {}

// WithPortMapping is a no-op for local runners -- same network namespace.
func (l *localCompanion) WithPortMapping(_ context.Context, _, _ uint16) {}

func (l *localCompanion) WithWorkDir(dir string) {
	l.workDir = dir
}

// WithPause is a no-op for local runners.
func (l *localCompanion) WithPause() {}

func (l *localCompanion) Init(ctx context.Context) error {
	return l.inner.Init(ctx)
}

func (l *localCompanion) NewProcess(bin string, args ...string) (Proc, error) {
	proc, err := l.inner.NewProcess(bin, args...)
	if err != nil {
		return nil, err
	}
	if l.workDir != "" {
		proc.WithDir(l.workDir)
	}
	return proc, nil
}

func (l *localCompanion) Shutdown(ctx context.Context) error {
	return l.inner.Shutdown(ctx)
}

func (l *localCompanion) RunnerEnv() RunnerEnvironment {
	return l.inner
}

func (l *localCompanion) Backend() Backend { return BackendLocal }

// --- Nix companion adapter ---

type nixCompanion struct {
	inner   *NixEnvironment
	workDir string
}

func newNixCompanion(ctx context.Context, opts CompanionOpts) (*nixCompanion, error) {
	env, err := NewNixEnvironment(ctx, opts.SourceDir)
	if err != nil {
		return nil, err
	}
	return &nixCompanion{inner: env, workDir: opts.SourceDir}, nil
}

// WithMount is a no-op for Nix runners -- host filesystem is already visible.
func (n *nixCompanion) WithMount(_, _ string) {}

// WithPortMapping is a no-op for Nix runners -- same network namespace.
func (n *nixCompanion) WithPortMapping(_ context.Context, _, _ uint16) {}

func (n *nixCompanion) WithWorkDir(dir string) {
	n.workDir = dir
}

// WithPause is a no-op for Nix runners.
func (n *nixCompanion) WithPause() {}

func (n *nixCompanion) Init(ctx context.Context) error {
	return n.inner.Init(ctx)
}

func (n *nixCompanion) NewProcess(bin string, args ...string) (Proc, error) {
	proc, err := n.inner.NewProcess(bin, args...)
	if err != nil {
		return nil, err
	}
	if n.workDir != "" {
		proc.WithDir(n.workDir)
	}
	return proc, nil
}

func (n *nixCompanion) Shutdown(ctx context.Context) error {
	return n.inner.Shutdown(ctx)
}

func (n *nixCompanion) RunnerEnv() RunnerEnvironment {
	return n.inner
}

func (n *nixCompanion) Backend() Backend { return BackendNix }
