package docker

import (
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
}

// TestBuilderAcceptsNormalizedArchitectureAlias pins the fix for the override
// escape hatch: an operator's `CODEFLY_BUILD_PLATFORM=linux/aarch64` builds an
// image that `docker image inspect` reports as "arm64"; the verification must
// treat those as the same architecture, not reject the correct image.
func TestBuilderAcceptsNormalizedArchitectureAlias(t *testing.T) {
	client := &scriptedBackend{architecture: "arm64"}
	builder := newTestBuilder(t, BuilderConfiguration{
		Root:        t.TempDir(),
		Dockerfile:  "Dockerfile",
		Destination: resources.NewDockerImage("test/alias:v1"),
		Platform:    "linux/aarch64",
		Output:      io.Discard,
	}, client)

	if _, err := builder.Build(context.Background()); err != nil {
		t.Fatalf("aarch64 alias build was wrongly rejected as a mismatch: %v", err)
	}
}

func TestBuilderPropagatesBuildFailure(t *testing.T) {
	client := &scriptedBackend{buildErr: errors.New("Dockerfile parse error: unknown instruction")}
	builder := newTestBuilder(t, BuilderConfiguration{
		Root:        t.TempDir(),
		Dockerfile:  "Dockerfile",
		Destination: resources.NewDockerImage("test/fail:v1"),
		Output:      io.Discard,
	}, client)

	if _, err := builder.Build(context.Background()); err == nil {
		t.Fatal("build failure unexpectedly reported success")
	}
	if client.calls != 1 {
		t.Fatalf("image build calls = %d, want 1", client.calls)
	}
}

func TestNormalizeArch(t *testing.T) {
	for arch, want := range map[string]string{
		"aarch64": "arm64",
		"arm64":   "arm64",
		"x86_64":  "amd64",
		"amd64":   "amd64",
		"armhf":   "arm",
		"i386":    "386",
		"ppc64le": "ppc64le",
	} {
		if got := normalizeArch(arch); got != want {
			t.Fatalf("normalizeArch(%q) = %q, want %q", arch, got, want)
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

// TestEnsureBuildxRequiresBuildxPlugin proves a buildx-less host fails with an
// actionable error naming the requirement, not the cryptic
// "'buildx' is not a docker command" the raw shell-out would surface.
func TestEnsureBuildxRequiresBuildxPlugin(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	err := ensureBuildx(context.Background())
	if err == nil {
		t.Fatal("expected an error when docker/buildx is unavailable")
	}
	if !strings.Contains(err.Error(), "buildx") {
		t.Fatalf("error must point at the buildx requirement, got: %v", err)
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

// scriptedBackend is a fake buildBackend. It records the platform of each
// build, returns a scripted build error, and reports a configurable
// architecture (defaulting to the requested one, i.e. a correct build).
type scriptedBackend struct {
	buildErr     error
	architecture string
	platforms    []string
	calls        int
}

func (b *scriptedBackend) Build(_ context.Context, req backendBuildRequest) error {
	b.platforms = append(b.platforms, req.Platform)
	b.calls++
	if b.buildErr != nil {
		return b.buildErr
	}
	_, _ = req.Output.Write([]byte("Step 1/1 : FROM scratch\n"))
	return nil
}

func (b *scriptedBackend) Architecture(_ context.Context, _ string) (string, error) {
	if b.architecture != "" {
		return b.architecture, nil
	}
	return platformArchitecture(b.platforms[len(b.platforms)-1]), nil
}
