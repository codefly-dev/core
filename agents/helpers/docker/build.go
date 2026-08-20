package docker

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

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
	Platform string
	Output   io.Writer
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
	Platform   string
	Dockerfile string
	Tag        string
	Context    []byte
	Output     io.Writer
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
	Image string
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

	buildContextBuffer, err := builder.createTarArchive(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot create tar archive")
	}
	buildContext := buildContextBuffer.Bytes()
	platform := builder.platform()

	if err := builder.build(ctx, platform, buildContext); err != nil {
		return nil, err
	}
	return &BuilderOutput{Image: builder.Destination.FullName()}, nil
}

func (builder *Builder) build(ctx context.Context, platform string, buildContext []byte) error {
	w := wool.Get(ctx).In("Builder.Build", wool.DirField(builder.Root))
	tag := builder.Destination.FullName()

	if err := builder.backend.Build(ctx, backendBuildRequest{
		Platform:   platform,
		Dockerfile: builder.Dockerfile,
		Tag:        tag,
		Context:    buildContext,
		Output:     builder.Output,
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
	if err := ensureBuildx(ctx); err != nil {
		return err
	}
	diagnostics := newBuildDiagnostics(8, 6000)
	tee := &lineDiagnosticsWriter{diagnostics: diagnostics}
	// os/exec calls Write from a single goroutine when Stdout and Stderr are
	// the same writer value, so sharing one MultiWriter is safe.
	out := io.MultiWriter(req.Output, tee)

	// --load places the single-platform result in the local image store so it
	// can be inspected and pushed. The context is the already
	// dockerignore-filtered tar, fed on stdin; -f names the Dockerfile inside
	// it.
	cmd := exec.CommandContext(ctx, "docker", "buildx", "build",
		"--platform", req.Platform,
		"--load",
		"--progress", "plain",
		"-f", req.Dockerfile,
		"-t", req.Tag,
		"-",
	)
	cmd.Stdin = bytes.NewReader(req.Context)
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

func (builder *Builder) readDockerignore(ctx context.Context) ([]string, error) {
	if builder.Ignorefile == "" {
		return nil, nil
	}
	w := wool.Get(ctx).In("Builder.readDockerignore", wool.DirField(builder.Root))
	ignoreFilePath := filepath.Join(builder.Root, builder.Ignorefile)
	file, err := os.Open(ignoreFilePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // No dockerignore file, nothing to ignore
		}
		return nil, err
	}
	defer file.Close()

	var patterns []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			patterns = append(patterns, line)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	w.Debug("patterns", wool.Field("patterns", patterns))
	return patterns, nil
}

func shouldIgnore(ctx context.Context, file string, patterns []string) bool {
	w := wool.Get(ctx).In("Builder.shouldIgnore", wool.Field("file", file), wool.Field("patterns", patterns))
	for _, pattern := range patterns {
		matched, err := filepath.Match(pattern, file)
		if err != nil {
			w.Focus("error", wool.ErrField(err))
			continue // Invalid pattern, skip it
		}
		if matched {
			return true
		}
	}
	return false
}

// createTarArchive creates a tar archive from the provided directory and returns it as a bytes buffer.
func (builder *Builder) createTarArchive(ctx context.Context) (*bytes.Buffer, error) {
	// Add a buffer to write our archive to.
	buf := new(bytes.Buffer)

	// Add a new tar archive.
	tw := tar.NewWriter(buf)

	patterns, err := builder.readDockerignore(ctx)
	if err != nil {
		return nil, err
	}

	// Walk through each file/folder in the path and add it to the tar archive.
	err = filepath.Walk(builder.Root, func(file string, fi os.FileInfo, err error) error {
		// Return any error.
		if err != nil {
			return err
		}

		// Add a new dir/file header.
		header, err := tar.FileInfoHeader(fi, file)
		if err != nil {
			return err
		}

		rel, err := filepath.Rel(builder.Root, file)
		if err != nil {
			return err
		}

		if shouldIgnore(ctx, rel, patterns) {
			return nil
		}

		header.Name = rel

		// Write the header.
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		if !fi.Mode().IsRegular() {
			return nil
		}

		// If it's not a directory, write the file content.
		if !fi.Mode().IsDir() {
			data, err := os.Open(file)
			if err != nil {
				return err
			}
			defer data.Close()

			if _, err := io.Copy(tw, data); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	// Make sure to check the error on Stop.
	if err := tw.Close(); err != nil {
		return nil, err
	}

	return buf, nil
}
