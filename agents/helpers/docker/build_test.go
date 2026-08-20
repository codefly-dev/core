package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runners/dockerrun"
)

func TestIsValidDockerImageName(t *testing.T) {
	tests := []struct {
		imageName string
		valid     bool
	}{
		{"examples/counter-go-grpc-nextjs-postgres/backend", true},
	}
	for _, tt := range tests {
		t.Run(tt.imageName, func(t *testing.T) {
			if got := IsValidDockerImageName(tt.imageName); got != tt.valid {
				t.Errorf("IsValidDockerImageName() = %v, want %v", got, tt.valid)
			}
		})
	}
}

func TestBuildDiagnosticsPreservesCauseAndBoundsOutput(t *testing.T) {
	diagnostics := newBuildDiagnostics(2, 120)
	diagnostics.Add("npm error package-lock is out of sync\n" + strings.Repeat("usage ", 50))
	diagnostics.Add("The command returned a non-zero code")
	got := diagnostics.String()
	if !strings.Contains(got, "package-lock is out of sync") || !strings.Contains(got, "non-zero code") {
		t.Fatalf("diagnostics lost actionable context: %q", got)
	}
	if len(got) > 120 {
		t.Fatalf("diagnostics length = %d, want at most 120", len(got))
	}
}

func TestBuilderRetriesOnlyLostLayerExportOnce(t *testing.T) {
	client := &scriptedBackend{
		buildErrs: []error{
			errors.New("failed to export image: failed to create image: failed to get layer sha256:abc: layer does not exist"),
			nil,
		},
	}
	builder := newTestBuilder(t, BuilderConfiguration{
		Root:        t.TempDir(),
		Dockerfile:  "Dockerfile",
		Destination: resources.NewDockerImage("test/retry:v1"),
		Output:      io.Discard,
	}, client)

	output, err := builder.Build(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if client.calls != 2 {
		t.Fatalf("image build calls = %d, want 2", client.calls)
	}
	if len(client.contexts) != 2 || !bytes.Equal(client.contexts[0], client.contexts[1]) {
		t.Fatal("retry did not reuse the exact immutable build context")
	}
	if output.Image != "test/retry:v1" {
		t.Fatalf("image = %q", output.Image)
	}
}

func TestBuilderDefaultsToLinuxAmd64Platform(t *testing.T) {
	client := &scriptedBackend{}
	builder := newTestBuilder(t, BuilderConfiguration{
		Root:        t.TempDir(),
		Dockerfile:  "Dockerfile",
		Destination: resources.NewDockerImage("test/platform:v1"),
		Output:      io.Discard,
	}, client)

	if _, err := builder.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := client.platforms[0]; got != "linux/amd64" {
		t.Fatalf("build platform = %q, want linux/amd64", got)
	}
}

func TestBuilderHonorsConfiguredPlatform(t *testing.T) {
	client := &scriptedBackend{}
	builder := newTestBuilder(t, BuilderConfiguration{
		Root:        t.TempDir(),
		Dockerfile:  "Dockerfile",
		Destination: resources.NewDockerImage("test/platform:v1"),
		Platform:    "linux/arm64",
		Output:      io.Discard,
	}, client)

	if _, err := builder.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := client.platforms[0]; got != "linux/arm64" {
		t.Fatalf("build platform = %q, want linux/arm64", got)
	}
}

func TestBuilderHonorsPlatformEnvironmentOverride(t *testing.T) {
	t.Setenv(BuildPlatformEnvironmentVariable, "linux/arm64")
	client := &scriptedBackend{}
	builder := newTestBuilder(t, BuilderConfiguration{
		Root:        t.TempDir(),
		Dockerfile:  "Dockerfile",
		Destination: resources.NewDockerImage("test/platform:v1"),
		Output:      io.Discard,
	}, client)

	if _, err := builder.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := client.platforms[0]; got != "linux/arm64" {
		t.Fatalf("build platform = %q, want linux/arm64 from %s", got, BuildPlatformEnvironmentVariable)
	}
}

func TestBuilderFailsOnArchitectureMismatch(t *testing.T) {
	// buildx honored nothing (or a future regression): the built image is the
	// wrong arch. The build must fail loudly rather than report success.
	client := &scriptedBackend{architecture: "arm64"}
	builder := newTestBuilder(t, BuilderConfiguration{
		Root:        t.TempDir(),
		Dockerfile:  "Dockerfile",
		Destination: resources.NewDockerImage("test/mismatch:v1"),
		Output:      io.Discard,
	}, client)

	_, err := builder.Build(context.Background())
	if err == nil {
		t.Fatal("wrong-architecture build unexpectedly reported success")
	}
	if !strings.Contains(err.Error(), "arm64") || !strings.Contains(err.Error(), "amd64") {
		t.Fatalf("mismatch error should name both architectures, got: %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("architecture mismatch must not retry: calls = %d, want 1", client.calls)
	}
}

func TestBuilderDoesNotRetryOrdinaryBuildFailure(t *testing.T) {
	client := &scriptedBackend{
		buildErrs: []error{errors.New("Dockerfile parse error: unknown instruction")},
	}
	builder := newTestBuilder(t, BuilderConfiguration{
		Root:        t.TempDir(),
		Dockerfile:  "Dockerfile",
		Destination: resources.NewDockerImage("test/no-retry:v1"),
		Output:      io.Discard,
	}, client)

	if _, err := builder.Build(context.Background()); err == nil {
		t.Fatal("ordinary Dockerfile failure unexpectedly succeeded")
	}
	if client.calls != 1 {
		t.Fatalf("image build calls = %d, want 1", client.calls)
	}
}

func TestRetryableDockerLayerExportErrorIsNarrow(t *testing.T) {
	retryable := errors.New("failed to export image: failed to create image: failed to get layer sha256:abc: layer does not exist")
	if !isRetryableDockerLayerExportError(retryable) {
		t.Fatal("lost-layer export was not classified as retryable")
	}
	for _, message := range []string{
		"layer does not exist",
		"failed to export image: disk quota exceeded",
		"failed to get layer: unauthorized",
	} {
		if isRetryableDockerLayerExportError(errors.New(message)) {
			t.Fatalf("ordinary failure %q was classified as retryable", message)
		}
	}
}

func TestPlatformArchitecture(t *testing.T) {
	for platform, want := range map[string]string{
		"linux/amd64":    "amd64",
		"linux/arm64":    "arm64",
		"linux/arm64/v8": "arm64",
		"amd64":          "amd64",
	} {
		if got := platformArchitecture(platform); got != want {
			t.Fatalf("platformArchitecture(%q) = %q, want %q", platform, got, want)
		}
	}
}

// TestBuilderBuildsRequestedArchitecture is the real-teeth test the fake-client
// assertions could never be: it builds a tiny image through the actual buildx
// path and inspects the result's architecture, so a regression to the
// platform-ignoring classic builder fails here instead of shipping silently.
func TestBuilderBuildsRequestedArchitecture(t *testing.T) {
	ctx := context.Background()
	if !dockerrun.DockerEngineRunning(ctx) {
		t.Skip("docker engine not reachable; skipping real image build")
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker CLI not on PATH; skipping real image build")
	}
	if err := exec.CommandContext(ctx, "docker", "buildx", "version").Run(); err != nil {
		t.Skip("docker buildx not available; skipping real image build")
	}

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "Dockerfile"), []byte("FROM alpine:3.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	image := resources.NewDockerImage("codefly-test/arch-check:v1")
	t.Cleanup(func() {
		_ = exec.Command("docker", "image", "rm", "-f", image.FullName()).Run()
	})

	builder, err := NewBuilder(BuilderConfiguration{
		Root:        root,
		Dockerfile:  "Dockerfile",
		Destination: image,
		Platform:    "linux/amd64",
		Output:      io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := builder.Build(ctx); err != nil {
		t.Fatalf("build failed: %v", err)
	}

	out, err := exec.CommandContext(ctx, "docker", "image", "inspect", image.FullName(), "--format", "{{.Architecture}}").Output()
	if err != nil {
		t.Fatalf("inspect built image: %v", err)
	}
	if arch := strings.TrimSpace(string(out)); arch != "amd64" {
		t.Fatalf("built image architecture = %q, want amd64", arch)
	}
}

func newTestBuilder(t *testing.T, cfg BuilderConfiguration, backend buildBackend) *Builder {
	t.Helper()
	builder, err := NewBuilder(cfg)
	if err != nil {
		t.Fatal(err)
	}
	builder.backend = backend
	return builder
}

// scriptedBackend is a fake buildBackend. It records the platform and context
// of each build, returns scripted per-call errors, and reports a configurable
// architecture (defaulting to the requested one, i.e. a correct build).
type scriptedBackend struct {
	buildErrs    []error
	architecture string
	platforms    []string
	contexts     [][]byte
	calls        int
}

func (b *scriptedBackend) Build(_ context.Context, req backendBuildRequest) error {
	b.platforms = append(b.platforms, req.Platform)
	b.contexts = append(b.contexts, req.Context)
	var err error
	if b.calls < len(b.buildErrs) {
		err = b.buildErrs[b.calls]
	}
	b.calls++
	if err == nil {
		_, _ = req.Output.Write([]byte("Step 1/1 : FROM scratch\n"))
	}
	return err
}

func (b *scriptedBackend) Architecture(_ context.Context, _ string) (string, error) {
	if b.architecture != "" {
		return b.architecture, nil
	}
	return platformArchitecture(b.platforms[len(b.platforms)-1]), nil
}
