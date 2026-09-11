package docker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/dockerrun"
	"github.com/codefly-dev/core/wool"
)

type Env struct {
	Key   string
	Value string
}

// DefaultBuildPlatform is the platform service images target when the caller
// does not specify one. Deployment images are pulled onto amd64 nodes, so the
// build must produce amd64 regardless of the host's native architecture —
// otherwise an image built on Apple Silicon (arm64) is pushed and then crashes
// with `exec format error` on the cluster.
const DefaultBuildPlatform = "linux/amd64"

// BuildPlatformEnvironmentVariable overrides the target build platform without
// a code change or a core release, for hosts deploying to a non-amd64 (e.g.
// arm64) node pool. It is only consulted when BuilderConfiguration.Platform is
// empty.
const BuildPlatformEnvironmentVariable = "CODEFLY_BUILD_PLATFORM"

type BuilderConfiguration struct {
	Root        string
	Dockerfile  string
	Ignorefile  string
	Destination *resources.DockerImage
	// Platform is the target build platform (e.g. "linux/amd64"). Empty means
	// BuildPlatformEnvironmentVariable, then DefaultBuildPlatform.
	Platform      string
	BuildxBuilder string
	Cache         *builderv0.BuildCacheOptions
	Output        io.Writer
}

type Builder struct {
	BuilderConfiguration
	backend buildBackend
}

// buildBackend runs an image build and reports a built image's architecture.
// The real implementation shells out to docker buildx (see dockerCLIBackend);
// tests inject a fake so the build-and-verify flow is exercised without a
// daemon.
type buildBackend interface {
	Build(ctx context.Context, req backendBuildRequest) error
	Architecture(ctx context.Context, image string) (string, error)
}

type backendBuildRequest struct {
	Platform      string
	BuildxBuilder string
	Dockerfile    string
	Tag           string
	Context       string
	Output        io.Writer
	Cache         *builderv0.BuildCacheOptions
}

func IsValidDockerImageName(_ string) bool {
	// Docker image name regex
	return true
}

func NewBuilder(cfg BuilderConfiguration) (*Builder, error) {
	return &Builder{
		BuilderConfiguration: cfg,
		backend:              dockerCLIBackend{},
	}, nil
}

type BuilderOutput struct {
	// Image is the fully-qualified tag of the image that was built.
	Image    string
	Duration time.Duration
}

// platform resolves the effective build platform: an explicit configuration
// wins, then the environment override, then the amd64 default.
func (builder *Builder) platform() string {
	if builder.Platform != "" {
		return builder.Platform
	}
	if env := strings.TrimSpace(os.Getenv(BuildPlatformEnvironmentVariable)); env != "" {
		return env
	}
	return DefaultBuildPlatform
}

func (builder *Builder) Build(ctx context.Context) (*BuilderOutput, error) {
	w := wool.Get(ctx).In("Builder.Build", wool.DirField(builder.Root))

	started := time.Now()
	if _, err := CacheArguments(builder.Cache, []string{builder.platform()}); err != nil {
		return nil, err
	}
	prepared, err := PrepareBuildContext(ctx, builder.Root, builder.Dockerfile, builder.Ignorefile)
	if err != nil {
		return nil, w.Wrapf(err, "prepare build context")
	}
	defer prepared.Close()
	platform := builder.platform()
	if err := builder.build(ctx, platform, prepared); err != nil {
		return nil, err
	}
	duration := time.Since(started)
	w.Info("image build completed", wool.Field("duration", duration))
	return &BuilderOutput{Image: builder.Destination.FullName(), Duration: duration}, nil
}

func (builder *Builder) build(ctx context.Context, platform string, prepared *PreparedBuildContext) error {
	w := wool.Get(ctx).In("Builder.Build", wool.DirField(builder.Root))
	tag := builder.Destination.FullName()

	if err := builder.backend.Build(ctx, backendBuildRequest{
		Platform:      platform,
		BuildxBuilder: builder.BuildxBuilder,
		Cache:         builder.Cache,
		Dockerfile:    prepared.Dockerfile,
		Tag:           tag,
		Context:       prepared.Root,
		Output:        builder.Output,
	}); err != nil {
		return err
	}

	// The classic Docker builder silently accepts the requested platform and
	// then produces a host-arch image anyway. buildx honors it, but a
	// wrong-arch image is the expensive failure — a crash-looping
	// `exec format error` once it reaches the cluster — so verify the built
	// image and fail loudly here rather than let a bad image get pushed and
	// digest-pinned downstream.
	arch, err := builder.backend.Architecture(ctx, tag)
	if err != nil {
		return w.Wrapf(err, "cannot inspect built image architecture")
	}
	if want := platformArchitecture(platform); normalizeArch(arch) != normalizeArch(want) {
		return w.NewError(
			"docker build produced a %q image but %q was requested (%s); it would fail with 'exec format error' on the target nodes",
			arch, want, platform,
		)
	}
	return nil
}

// platformArchitecture extracts the architecture component of an OCI platform
// string ("linux/amd64" → "amd64", "linux/arm64/v8" → "arm64").
func platformArchitecture(platform string) string {
	parts := strings.Split(platform, "/")
	if len(parts) >= 2 {
		return parts[1]
	}
	return platform
}

// normalizeArch maps the common architecture aliases an operator might type
// (e.g. `uname -m`'s "aarch64"/"x86_64") to the canonical GOARCH-style names
// that `docker image inspect` reports. Without this, a correctly built image
// from `CODEFLY_BUILD_PLATFORM=linux/aarch64` inspects as "arm64" and the
// verification would reject it as a bogus mismatch.
func normalizeArch(arch string) string {
	switch arch {
	case "x86_64", "x86-64":
		return "amd64"
	case "aarch64", "arm64v8":
		return "arm64"
	case "armhf", "armel":
		return "arm"
	case "i386", "i686", "x86":
		return "386"
	default:
		return arch
	}
}

// dockerCLIBackend builds through the docker CLI's buildx builder. The Go SDK's
// classic /build endpoint discards ImageBuildOptions.Platform; only BuildKit
// (buildx) honors it, so the fix has to route through buildx rather than the
// SDK.
type dockerCLIBackend struct{}

func (dockerCLIBackend) Build(ctx context.Context, req backendBuildRequest) error {
	cacheArgs, err := CacheArguments(req.Cache, []string{req.Platform})
	if err != nil {
		return err
	}
	if err := ensureBuildx(ctx); err != nil {
		return err
	}
	diagnostics := newBuildDiagnostics(8, 6000)
	tee := &lineDiagnosticsWriter{diagnostics: diagnostics}
	// os/exec calls Write from a single goroutine when Stdout and Stderr are
	// the same writer value, so sharing one MultiWriter is safe.
	out := io.MultiWriter(req.Output, tee)

	// Local context transfer keeps unused inputs out of the exported cache graph.
	args := []string{"buildx", "build",
		"--platform", req.Platform,
		"--load",
		"--progress", "plain",
		"-f", req.Dockerfile,
		"-t", req.Tag,
	}
	if req.BuildxBuilder != "" {
		args = append(args, "--builder", req.BuildxBuilder)
	}
	args = append(args, cacheArgs...)
	args = append(args, req.Context)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = out
	cmd.Stderr = out

	if err := cmd.Run(); err != nil {
		tee.flush()
		if detail := diagnostics.String(); detail != "" {
			return fmt.Errorf("docker build failed: %w\nlast build output:\n%s", err, detail)
		}
		return fmt.Errorf("docker build failed: %w", err)
	}
	return nil
}

func (dockerCLIBackend) Architecture(ctx context.Context, image string) (string, error) {
	// Inspect through the SDK (like dockerrun.GetImageID) rather than a second
	// `docker` shell-out: it reuses the context-aware client resolution in
	// dockerrun and keeps the CLI dependency confined to the build itself,
	// which genuinely needs buildx.
	cli, err := dockerrun.NewClient()
	if err != nil {
		return "", fmt.Errorf("create docker client: %w", err)
	}
	defer cli.Close()
	inspect, _, err := cli.ImageInspectWithRaw(ctx, image)
	if err != nil {
		return "", fmt.Errorf("inspect %s: %w", image, err)
	}
	return inspect.Architecture, nil
}

// ensureBuildx fails with an actionable error when the docker CLI or its buildx
// plugin is missing. The build routes through buildx because the classic
// builder silently ignores --platform and produces wrong-architecture images;
// without this probe a buildx-less host (e.g. Debian's `docker.io` package, or
// a socket-only environment) fails the build with a cryptic
// "'buildx' is not a docker command" instead of a clear requirement.
func ensureBuildx(ctx context.Context) error {
	if _, err := exec.LookPath("docker"); err != nil {
		return fmt.Errorf("docker CLI not found on PATH: building service images requires Docker with the buildx plugin so the target --platform is honored: %w", err)
	}
	if err := exec.CommandContext(ctx, "docker", "buildx", "version").Run(); err != nil {
		return fmt.Errorf("docker buildx plugin unavailable: building service images requires buildx so the target --platform is honored (the classic builder ignores it); install the docker-buildx-plugin: %w", err)
	}
	return nil
}

// lineDiagnosticsWriter splits streamed build output into whole lines and feeds
// them to buildDiagnostics, so a failing build can surface a bounded tail of
// its output in the returned error.
type lineDiagnosticsWriter struct {
	diagnostics *buildDiagnostics
	partial     []byte
}

func (w *lineDiagnosticsWriter) Write(p []byte) (int, error) {
	w.partial = append(w.partial, p...)
	for {
		i := bytes.IndexByte(w.partial, '\n')
		if i < 0 {
			break
		}
		w.diagnostics.Add(string(w.partial[:i]))
		w.partial = w.partial[i+1:]
	}
	return len(p), nil
}

func (w *lineDiagnosticsWriter) flush() {
	if len(w.partial) > 0 {
		w.diagnostics.Add(string(w.partial))
		w.partial = nil
	}
}

type buildDiagnostics struct {
	maxChunks int
	maxBytes  int
	chunks    []string
}

func newBuildDiagnostics(maxChunks, maxBytes int) *buildDiagnostics {
	return &buildDiagnostics{maxChunks: maxChunks, maxBytes: maxBytes}
}

func (diagnostics *buildDiagnostics) Add(output string) {
	if diagnostics == nil || diagnostics.maxChunks <= 0 || diagnostics.maxBytes <= 0 {
		return
	}
	output = strings.TrimSpace(output)
	if output == "" {
		return
	}
	if len(output) > diagnostics.maxBytes {
		// Build tools commonly emit the actual cause first, followed by long
		// usage/help text. Preserve that cause and a small ending for context.
		end := diagnostics.maxBytes / 4
		marker := "\n… output truncated …\n"
		start := diagnostics.maxBytes - end - len(marker)
		if start < 0 {
			start = 0
		}
		output = output[:start] + marker + output[len(output)-end:]
	}
	diagnostics.chunks = append(diagnostics.chunks, output)
	if len(diagnostics.chunks) > diagnostics.maxChunks {
		diagnostics.chunks = append([]string(nil), diagnostics.chunks[len(diagnostics.chunks)-diagnostics.maxChunks:]...)
	}
}

func (diagnostics *buildDiagnostics) String() string {
	if diagnostics == nil {
		return ""
	}
	joined := strings.Join(diagnostics.chunks, "\n")
	if len(joined) <= diagnostics.maxBytes {
		return joined
	}
	end := diagnostics.maxBytes / 4
	marker := "\n… output truncated …\n"
	start := diagnostics.maxBytes - end - len(marker)
	if start < 0 {
		start = 0
	}
	return joined[:start] + marker + joined[len(joined)-end:]
}
