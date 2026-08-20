package docker

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/api/types"
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
	client := &scriptedImageBuildClient{
		responses: []string{
			`{"errorDetail":{"message":"failed to export image: failed to create image: failed to get layer sha256:abc: layer does not exist"}}` + "\n",
			`{"stream":"Step 1/1 : FROM scratch\n"}` + "\n",
		},
	}
	builder, err := NewBuilder(BuilderConfiguration{
		Root:        t.TempDir(),
		Dockerfile:  "Dockerfile",
		Destination: resources.NewDockerImage("test/retry:v1"),
		Output:      io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	builder.newClient = func() (imageBuildClient, error) { return client, nil }

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
	if !client.closed {
		t.Fatal("docker client was not closed")
	}
}

func TestBuilderDefaultsToLinuxAmd64Platform(t *testing.T) {
	client := &scriptedImageBuildClient{
		responses: []string{`{"stream":"Step 1/1 : FROM scratch\n"}` + "\n"},
	}
	builder, err := NewBuilder(BuilderConfiguration{
		Root:        t.TempDir(),
		Dockerfile:  "Dockerfile",
		Destination: resources.NewDockerImage("test/platform:v1"),
		Output:      io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	builder.newClient = func() (imageBuildClient, error) { return client, nil }

	if _, err := builder.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := client.platforms[0]; got != "linux/amd64" {
		t.Fatalf("build platform = %q, want linux/amd64", got)
	}
}

func TestBuilderHonorsConfiguredPlatform(t *testing.T) {
	client := &scriptedImageBuildClient{
		responses: []string{`{"stream":"Step 1/1 : FROM scratch\n"}` + "\n"},
	}
	builder, err := NewBuilder(BuilderConfiguration{
		Root:        t.TempDir(),
		Dockerfile:  "Dockerfile",
		Destination: resources.NewDockerImage("test/platform:v1"),
		Platform:    "linux/arm64",
		Output:      io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	builder.newClient = func() (imageBuildClient, error) { return client, nil }

	if _, err := builder.Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := client.platforms[0]; got != "linux/arm64" {
		t.Fatalf("build platform = %q, want linux/arm64", got)
	}
}

func TestBuilderDoesNotRetryOrdinaryBuildFailure(t *testing.T) {
	client := &scriptedImageBuildClient{
		responses: []string{
			`{"errorDetail":{"message":"Dockerfile parse error: unknown instruction"}}` + "\n",
		},
	}
	builder, err := NewBuilder(BuilderConfiguration{
		Root:        t.TempDir(),
		Dockerfile:  "Dockerfile",
		Destination: resources.NewDockerImage("test/no-retry:v1"),
		Output:      io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	builder.newClient = func() (imageBuildClient, error) { return client, nil }

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

type scriptedImageBuildClient struct {
	responses []string
	contexts  [][]byte
	platforms []string
	calls     int
	closed    bool
}

func (client *scriptedImageBuildClient) ImageBuild(
	_ context.Context,
	buildContext io.Reader,
	options types.ImageBuildOptions,
) (types.ImageBuildResponse, error) {
	payload, err := io.ReadAll(buildContext)
	if err != nil {
		return types.ImageBuildResponse{}, err
	}
	client.contexts = append(client.contexts, payload)
	client.platforms = append(client.platforms, options.Platform)
	if client.calls >= len(client.responses) {
		return types.ImageBuildResponse{}, errors.New("unexpected image build call")
	}
	response := client.responses[client.calls]
	client.calls++
	return types.ImageBuildResponse{Body: io.NopCloser(bytes.NewBufferString(response))}, nil
}

func (client *scriptedImageBuildClient) Close() error {
	client.closed = true
	return nil
}
