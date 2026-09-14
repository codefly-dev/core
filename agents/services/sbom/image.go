package sbom

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"google.golang.org/protobuf/proto"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
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

// SourceOf reports how a subject's image has to be reached. Nothing in the
// reference or the digest reveals it — a local image ID and a registry
// manifest digest are both "sha256:<hex>", and a loaded image commonly carries
// the tag a pushed one would — so only what the subject declares can decide.
// An unset kind means registry, so subjects written before the selector existed
// resolve the way they always did.
func SourceOf(subject *builderv0.ImageSubject) ImageSource {
	if subject.GetSource() == builderv0.ImageSourceKind_IMAGE_SOURCE_KIND_DOCKER_DAEMON {
		return SourceDockerDaemon
	}
	return SourceRegistry
}

// SourceKind is the wire selector for a scanner source, so a caller that
// resolved an image from its own build states on the subject what it resolved.
func SourceKind(source ImageSource) builderv0.ImageSourceKind {
	if source == SourceDockerDaemon {
		return builderv0.ImageSourceKind_IMAGE_SOURCE_KIND_DOCKER_DAEMON
	}
	return builderv0.ImageSourceKind_IMAGE_SOURCE_KIND_REGISTRY
}

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
		return inspectDaemonImage(ctx, req)
	}
	pinned := referenceDigest(req.Reference)
	if !dockerAvailable() {
		// Resolving a tag, or choosing one platform's child manifest, needs a
		// registry client. A fully pinned reference is still scannable by syft
		// alone, so that case must not require docker.
		if pinned == "" || req.Platform != "" {
			return "", "", fmt.Errorf("%w: resolving %s needs docker to query the registry; pin the reference to a digest and omit the platform to scan with syft alone", ErrUnsupported, req.Reference)
		}
		return pinned, "", nil
	}
	raw, err := runCommand(ctx, "docker", "buildx", "imagetools", "inspect", req.Reference, "--format", "{{json .Manifest}}")
	if err != nil {
		return "", "", fmt.Errorf("resolve registry digest for %s: %w", req.Reference, err)
	}
	digest, platform, err := selectManifestDigest(raw, req.Platform)
	if err != nil {
		return "", "", fmt.Errorf("resolve registry digest for %s: %w", req.Reference, err)
	}
	if req.Platform != "" && platform == "" {
		// A single manifest descriptor carries no platform field, so the
		// registry has not confirmed the caller's request. Read the image
		// config instead of labelling the evidence on trust.
		if err := verifyConfigPlatform(ctx, pinnedReference(req.Reference, digest), req.Platform); err != nil {
			return "", "", err
		}
		platform = req.Platform
	}
	return digest, platform, nil
}

func dockerAvailable() bool {
	_, err := exec.LookPath("docker")
	return err == nil
}

// inspectDaemonImage reports the local image ID, which is the only immutable
// identity an image that was never pushed has.
func inspectDaemonImage(ctx context.Context, req ImageRequest) (string, string, error) {
	raw, err := runCommand(ctx, "docker", "image", "inspect", req.Reference, "--format", "{{.Id}} {{.Os}}/{{.Architecture}}")
	if err != nil {
		return "", "", fmt.Errorf("resolve local image %s: %w", req.Reference, err)
	}
	return daemonIdentity(raw, req.Reference, req.Platform)
}

// daemonIdentity parses docker's identity line and refuses an image whose
// platform is not the one that was asked for. The local daemon holds one
// platform per reference, so a mismatch means the wrong image would be scanned.
func daemonIdentity(raw []byte, reference, want string) (string, string, error) {
	fields := strings.Fields(string(raw))
	if len(fields) != 2 || !strings.HasPrefix(fields[0], "sha256:") {
		return "", "", fmt.Errorf("local image %s reported no usable image ID", reference)
	}
	if want != "" && fields[1] != want {
		return "", "", fmt.Errorf("local image %s is %s, not the requested platform %s", reference, fields[1], want)
	}
	return fields[0], fields[1], nil
}

// verifyConfigPlatform reads the image config to confirm a platform the
// manifest descriptor did not state.
func verifyConfigPlatform(ctx context.Context, reference, want string) error {
	raw, err := runCommand(ctx, "docker", "buildx", "imagetools", "inspect", reference, "--format", "{{json .Image}}")
	if err != nil {
		return fmt.Errorf("verify platform of %s: %w", reference, err)
	}
	got, err := configPlatform(raw, want)
	if err != nil {
		return fmt.Errorf("verify platform of %s: %w", reference, err)
	}
	if got != want {
		return fmt.Errorf("image %s is %s, not the requested platform %s", reference, got, want)
	}
	return nil
}

// configPlatform reads os/architecture from an image config. buildx emits a
// bare config for a single-platform reference and a map keyed by "os/arch" for
// a multi-platform one.
func configPlatform(data []byte, want string) (string, error) {
	var single manifestPlatform
	if err := json.Unmarshal(data, &single); err == nil && single.String() != "" {
		return single.String(), nil
	}
	byPlatform := map[string]manifestPlatform{}
	if err := json.Unmarshal(data, &byPlatform); err != nil {
		return "", fmt.Errorf("parse image config: %w", err)
	}
	if entry, ok := byPlatform[want]; ok && entry.String() != "" {
		return entry.String(), nil
	}
	return "", fmt.Errorf("image config declares no platform %s", want)
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
	if len(descriptor.Manifests) > 0 {
		// An index whose every child is an attestation has nothing scannable.
		// Falling through would bind evidence to the index digest itself.
		return "", "", fmt.Errorf("image index lists no shipped platform manifest")
	}
	if descriptor.Digest == "" {
		return "", "", fmt.Errorf("image manifest carries no digest")
	}
	resolved := descriptor.Platform.String()
	if platform != "" && resolved != "" && resolved != platform {
		return "", "", fmt.Errorf("image platform %s does not match requested platform %s", resolved, platform)
	}
	// An unstated platform stays unstated. The caller confirms it against the
	// image config rather than having its own request echoed back as evidence.
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
	source := base.Bom.GetMetadata().GetComponent()
	if source == nil {
		return nil, fmt.Errorf("image SBOM has no root component")
	}
	// Clone rather than mutate: the caller still owns base, and appending a
	// hash to its root twice would silently change the document digest.
	root := proto.Clone(source).(*agentv0.Component)
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
