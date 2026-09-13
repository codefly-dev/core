package sbom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
)

// ImageSource selects how the scanner reaches an image.
type ImageSource int

const (
	// SourceRegistry resolves and scans the image through its registry.
	SourceRegistry ImageSource = iota
	// SourceDockerDaemon scans an image held by the local Docker daemon. It is
	// the supported path for an image a build produced but never pushed, whose
	// only immutable identity is its local image ID.
	SourceDockerDaemon
)

// ImageRequest is one image and platform to inventory.
type ImageRequest struct {
	// Reference is the image reference. A tag is resolved to a digest before
	// scanning so the inventory describes exactly one immutable image.
	Reference string
	// Platform selects one platform of a multi-architecture image, in "os/arch"
	// form. It is required when the image carries more than one platform.
	Platform string
	// Source selects the registry or the local Docker daemon.
	Source ImageSource
}

// ImageResult is an image inventory bound to the digest actually scanned.
type ImageResult struct {
	*Result
	Reference string
	Digest    string
	Platform  string
}

// Image inventories the OS packages and installed application dependencies of
// one image and binds the evidence to the immutable digest it was scanned
// from. It never returns a result whose digest is unknown: evidence that is not
// bound to a digest cannot be matched against a deployed image.
func Image(ctx context.Context, req ImageRequest) (*ImageResult, error) {
	if strings.TrimSpace(req.Reference) == "" {
		return nil, fmt.Errorf("image SBOM requires an image reference")
	}
	digest, platform, err := resolveImage(ctx, req)
	if err != nil {
		return nil, err
	}
	scanned, err := scanImage(ctx, req, digest)
	if err != nil {
		return nil, err
	}
	bound, err := bindImageDigest(scanned, digest)
	if err != nil {
		return nil, err
	}
	return &ImageResult{Result: bound, Reference: req.Reference, Digest: digest, Platform: platform}, nil
}

func resolveImage(ctx context.Context, req ImageRequest) (string, string, error) {
	if req.Source == SourceDockerDaemon {
		return inspectDaemonImage(ctx, req.Reference)
	}
	raw, err := runCommand(ctx, "docker", "buildx", "imagetools", "inspect", req.Reference, "--format", "{{json .Manifest}}")
	if err != nil {
		return "", "", fmt.Errorf("resolve registry digest for %s: %w", req.Reference, err)
	}
	digest, platform, err := selectManifestDigest(raw, req.Platform)
	if err != nil {
		return "", "", fmt.Errorf("resolve registry digest for %s: %w", req.Reference, err)
	}
	return digest, platform, nil
}

// inspectDaemonImage reports the local image ID, which is the only immutable
// identity an image that was never pushed has.
func inspectDaemonImage(ctx context.Context, reference string) (string, string, error) {
	raw, err := runCommand(ctx, "docker", "image", "inspect", reference, "--format", "{{.Id}} {{.Os}}/{{.Architecture}}")
	if err != nil {
		return "", "", fmt.Errorf("resolve local image %s: %w", reference, err)
	}
	fields := strings.Fields(string(raw))
	if len(fields) != 2 || !strings.HasPrefix(fields[0], "sha256:") {
		return "", "", fmt.Errorf("local image %s reported no usable image ID", reference)
	}
	return fields[0], fields[1], nil
}

type manifestDescriptor struct {
	Digest    string             `json:"digest"`
	Platform  *manifestPlatform  `json:"platform"`
	Manifests []manifestChildRef `json:"manifests"`
}

type manifestChildRef struct {
	Digest   string            `json:"digest"`
	Platform *manifestPlatform `json:"platform"`
}

type manifestPlatform struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}

func (platform *manifestPlatform) String() string {
	if platform == nil || platform.OS == "" || platform.Architecture == "" {
		return ""
	}
	return platform.OS + "/" + platform.Architecture
}

// selectManifestDigest picks the exact manifest an inventory will describe. A
// multi-platform image with no requested platform is an error rather than a
// silent default: scanning one arbitrary platform of a multi-architecture image
// and reporting it as coverage is precisely the gap this contract closes.
func selectManifestDigest(data []byte, platform string) (string, string, error) {
	var descriptor manifestDescriptor
	if err := json.Unmarshal(data, &descriptor); err != nil {
		return "", "", fmt.Errorf("parse image manifest: %w", err)
	}
	shipped := make([]manifestChildRef, 0, len(descriptor.Manifests))
	for _, child := range descriptor.Manifests {
		// Buildx indexes carry attestation manifests alongside the shipped
		// platforms; they declare unknown/unknown and are not scan subjects.
		if child.Platform.String() != "" && child.Platform.String() != "unknown/unknown" {
			shipped = append(shipped, child)
		}
	}
	if len(shipped) > 0 {
		if platform == "" {
			if len(shipped) > 1 {
				return "", "", fmt.Errorf("image ships %d platforms; name the platform to scan", len(shipped))
			}
			return shipped[0].Digest, shipped[0].Platform.String(), nil
		}
		for _, child := range shipped {
			if child.Platform.String() == platform {
				return child.Digest, platform, nil
			}
		}
		return "", "", fmt.Errorf("image ships no manifest for platform %s", platform)
	}
	if descriptor.Digest == "" {
		return "", "", fmt.Errorf("image manifest carries no digest")
	}
	resolved := descriptor.Platform.String()
	if platform != "" && resolved != "" && resolved != platform {
		return "", "", fmt.Errorf("image platform %s does not match requested platform %s", resolved, platform)
	}
	if resolved == "" {
		resolved = platform
	}
	return descriptor.Digest, resolved, nil
}

func scanImage(ctx context.Context, req ImageRequest, digest string) (*Result, error) {
	if req.Source == SourceDockerDaemon {
		// The managed scanner runs without the Docker socket by design, so it
		// cannot reach the local daemon. A host syft is the only supported way
		// to inventory an image that was never pushed.
		if _, err := exec.LookPath("syft"); err != nil {
			return nil, fmt.Errorf("%w: scanning a local-only image requires an installed syft; the managed scanner has no access to the Docker daemon", ErrUnsupported)
		}
		return runSyft(ctx, "syft", []string{"docker:" + req.Reference, "-o", "cyclonedx-json@1.5"}, "syft", req.Reference)
	}
	target := "registry:" + pinnedReference(req.Reference, digest)
	if _, err := exec.LookPath("syft"); err != nil {
		if _, dockerErr := exec.LookPath("docker"); dockerErr != nil {
			return nil, fmt.Errorf("%w: neither syft nor docker is installed", ErrUnsupported)
		}
		return runSyft(ctx, "docker", managedSyftArgsFor(target), "syft@"+SyftVersion, req.Reference)
	}
	return runSyft(ctx, "syft", []string{target, "-o", "cyclonedx-json@1.5"}, "syft", req.Reference)
}

func runSyft(ctx context.Context, name string, args []string, tool, image string) (*Result, error) {
	stdout, stderr, err := execute(ctx, name, args...)
	if err != nil {
		return nil, wrapSyftError(tool, image, err, stderr)
	}
	return parseCycloneDX(stdout, tool, "DOCKER")
}

// pinnedReference replaces any tag or digest with the resolved digest so the
// scan cannot drift to a different image between resolution and inventory.
func pinnedReference(reference, digest string) string {
	return referenceName(reference) + "@" + digest
}

// bindImageDigest makes the scanned digest part of the document itself, so an
// exported CycloneDX artifact still names the image it describes.
func bindImageDigest(base *Result, digest string) (*Result, error) {
	root := base.Bom.GetMetadata().GetComponent()
	if root == nil {
		return nil, fmt.Errorf("image SBOM has no root component")
	}
	root.Type = agentv0.ComponentType_CONTAINER
	root.Hashes = append(root.Hashes, &agentv0.Hash{
		Algorithm: "SHA-256",
		Content:   strings.ToLower(strings.TrimPrefix(digest, "sha256:")),
	})
	return finish(root, base.Bom.GetComponents(), base.Bom.GetDependencies(), base.Tool, base.Language)
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	stdout, stderr, err := execute(ctx, name, args...)
	if err != nil {
		return nil, fmt.Errorf("%s: %w: %s", name, err, stderr)
	}
	return stdout, nil
}

func execute(ctx context.Context, name string, args ...string) ([]byte, string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), strings.TrimSpace(stderr.String()), err
}
