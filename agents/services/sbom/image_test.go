package sbom

import (
	"strings"
	"testing"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	"github.com/stretchr/testify/require"
)

// realBuildxIndex is the shape `docker buildx imagetools inspect --format
// "{{json .Manifest}}"` actually emits for a multi-platform image, annotations
// and all. The hand-written fixtures below are only trustworthy if the parser
// also accepts the real thing.
const realBuildxIndex = `{
  "schemaVersion": 2,
  "mediaType": "application/vnd.oci.image.index.v1+json",
  "digest": "sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc",
  "size": 9226,
  "manifests": [
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:c64c687cbea9300178b30c95835354e34c4e4febc4badfe27102879de0483b5e",
      "size": 1023,
      "annotations": {"com.docker.official-images.bashbrew.arch": "amd64"},
      "platform": {"architecture": "amd64", "os": "linux"}
    },
    {
      "mediaType": "application/vnd.oci.image.manifest.v1+json",
      "digest": "sha256:6243dec9286873e0392f0527f96c394684dc6fa12661f0bd1925abcdef012345",
      "size": 1023,
      "platform": {"architecture": "arm64", "os": "linux"}
    }
  ]
}`

func TestSelectManifestDigestParsesRealBuildxIndex(t *testing.T) {
	digest, platform, err := selectManifestDigest([]byte(realBuildxIndex), "linux/arm64")
	require.NoError(t, err)
	require.Equal(t, "sha256:6243dec9286873e0392f0527f96c394684dc6fa12661f0bd1925abcdef012345", digest)
	require.Equal(t, "linux/arm64", platform)
}

// A digest-pinned single manifest carries no platform field at all. The
// resolver must leave it unstated rather than echo the request back, or
// evidence would claim a platform nothing verified.
func TestSelectManifestDigestLeavesAnUnstatedPlatformUnstated(t *testing.T) {
	descriptor := `{"mediaType":"application/vnd.oci.image.manifest.v1+json","digest":"sha256:c64c","size":1023}`
	digest, platform, err := selectManifestDigest([]byte(descriptor), "linux/amd64")
	require.NoError(t, err)
	require.Equal(t, "sha256:c64c", digest)
	require.Empty(t, platform)
}

func TestSelectManifestDigestRejectsAnAttestationOnlyIndex(t *testing.T) {
	index := `{"digest":"sha256:index","manifests":[
		{"digest":"sha256:att","platform":{"os":"unknown","architecture":"unknown"}}]}`
	_, _, err := selectManifestDigest([]byte(index), "linux/amd64")
	require.ErrorContains(t, err, "no shipped platform manifest")
}

func TestSelectManifestDigestPicksThePlatformChild(t *testing.T) {
	index := `{"digest":"sha256:index","manifests":[
		{"digest":"sha256:amd","platform":{"os":"linux","architecture":"amd64"}},
		{"digest":"sha256:arm","platform":{"os":"linux","architecture":"arm64"}}]}`
	digest, platform, err := selectManifestDigest([]byte(index), "linux/arm64")
	require.NoError(t, err)
	require.Equal(t, "sha256:arm", digest)
	require.Equal(t, "linux/arm64", platform)
}

func TestSelectManifestDigestRefusesAmbiguousMultiPlatformImage(t *testing.T) {
	index := `{"digest":"sha256:index","manifests":[
		{"digest":"sha256:amd","platform":{"os":"linux","architecture":"amd64"}},
		{"digest":"sha256:arm","platform":{"os":"linux","architecture":"arm64"}}]}`
	_, _, err := selectManifestDigest([]byte(index), "")
	require.ErrorContains(t, err, "name the platform")
}

func TestSelectManifestDigestIgnoresAttestationManifests(t *testing.T) {
	index := `{"digest":"sha256:index","manifests":[
		{"digest":"sha256:amd","platform":{"os":"linux","architecture":"amd64"}},
		{"digest":"sha256:att","platform":{"os":"unknown","architecture":"unknown"}}]}`
	digest, platform, err := selectManifestDigest([]byte(index), "")
	require.NoError(t, err)
	require.Equal(t, "sha256:amd", digest)
	require.Equal(t, "linux/amd64", platform)
}

func TestSelectManifestDigestRejectsAnUnshippedPlatform(t *testing.T) {
	index := `{"digest":"sha256:index","manifests":[
		{"digest":"sha256:amd","platform":{"os":"linux","architecture":"amd64"}}]}`
	_, _, err := selectManifestDigest([]byte(index), "linux/arm64")
	require.ErrorContains(t, err, "no manifest for platform linux/arm64")
}

func TestSelectManifestDigestRejectsPlatformMismatch(t *testing.T) {
	_, _, err := selectManifestDigest([]byte(`{"digest":"sha256:only","platform":{"os":"linux","architecture":"amd64"}}`), "linux/arm64")
	require.ErrorContains(t, err, "does not match requested platform")
}

func TestSelectManifestDigestUsesASingleManifest(t *testing.T) {
	digest, platform, err := selectManifestDigest([]byte(`{"digest":"sha256:only","platform":{"os":"linux","architecture":"amd64"}}`), "")
	require.NoError(t, err)
	require.Equal(t, "sha256:only", digest)
	require.Equal(t, "linux/amd64", platform)
}

// buildx emits a bare config for a single-platform reference and a map keyed by
// "os/arch" for a multi-platform one.
func TestConfigPlatformReadsBothBuildxShapes(t *testing.T) {
	single := `{"created":"2026-04-16T23:53:26Z","architecture":"amd64","os":"linux","rootfs":{"type":"layers"}}`
	got, err := configPlatform([]byte(single), "linux/amd64")
	require.NoError(t, err)
	require.Equal(t, "linux/amd64", got)

	multi := `{"linux/386":{"architecture":"386","os":"linux"},"linux/arm64":{"architecture":"arm64","os":"linux"}}`
	got, err = configPlatform([]byte(multi), "linux/arm64")
	require.NoError(t, err)
	require.Equal(t, "linux/arm64", got)

	_, err = configPlatform([]byte(multi), "windows/amd64")
	require.ErrorContains(t, err, "declares no platform windows/amd64")
}

func TestConfigPlatformDetectsAMismatchedSingleConfig(t *testing.T) {
	single := `{"architecture":"amd64","os":"linux"}`
	got, err := configPlatform([]byte(single), "linux/arm64")
	require.NoError(t, err)
	require.Equal(t, "linux/amd64", got, "caller compares and rejects the mismatch")
}

// The local daemon holds one platform per reference, so scanning a reference
// whose platform differs from the request would inventory the wrong image.
func TestDaemonIdentityRejectsAPlatformMismatch(t *testing.T) {
	_, _, err := daemonIdentity([]byte("sha256:abc linux/amd64\n"), "app:dev", "linux/arm64")
	require.ErrorContains(t, err, "not the requested platform linux/arm64")

	id, platform, err := daemonIdentity([]byte("sha256:abc linux/amd64\n"), "app:dev", "linux/amd64")
	require.NoError(t, err)
	require.Equal(t, "sha256:abc", id)
	require.Equal(t, "linux/amd64", platform)

	id, platform, err = daemonIdentity([]byte("sha256:abc linux/amd64\n"), "app:dev", "")
	require.NoError(t, err)
	require.Equal(t, "sha256:abc", id)
	require.Equal(t, "linux/amd64", platform)
}

func TestDaemonIdentityRejectsUnusableOutput(t *testing.T) {
	_, _, err := daemonIdentity([]byte("<no value> linux/amd64"), "app:dev", "")
	require.ErrorContains(t, err, "no usable image ID")
}

func TestPinnedReferenceReplacesTagAndDigest(t *testing.T) {
	require.Equal(t, "ghcr.io/codefly-dev/api@sha256:new", pinnedReference("ghcr.io/codefly-dev/api:1.2.3", "sha256:new"))
	require.Equal(t, "ghcr.io/codefly-dev/api@sha256:new", pinnedReference("ghcr.io/codefly-dev/api@sha256:old", "sha256:new"))
	require.Equal(t, "localhost:5000/api@sha256:new", pinnedReference("localhost:5000/api:dev", "sha256:new"))
	require.Equal(t, "redis@sha256:new", pinnedReference("redis", "sha256:new"))
}

func TestBindImageDigestMakesTheImageTheSubject(t *testing.T) {
	root := &agentv0.Component{Name: "redis", Type: agentv0.ComponentType_MODULE, BomRef: "root"}
	packages := []*agentv0.Component{{Name: "openssl", Version: "3.0", BomRef: "pkg:deb/openssl@3.0"}}
	base, err := finish(root, packages, nil, "syft", "DOCKER")
	require.NoError(t, err)

	bound, err := bindImageDigest(base, "sha256:ABCDEF")
	require.NoError(t, err)
	subject := bound.Bom.GetMetadata().GetComponent()
	require.Equal(t, agentv0.ComponentType_CONTAINER, subject.GetType())
	require.Equal(t, "SHA-256", subject.GetHashes()[0].GetAlgorithm())
	require.Equal(t, "abcdef", subject.GetHashes()[0].GetContent())
	require.Len(t, bound.Bom.GetComponents(), 1)
}

// Binding must not mutate the caller's document: a second bind would otherwise
// accumulate hashes and silently change the digest.
func TestBindImageDigestDoesNotMutateItsInput(t *testing.T) {
	root := &agentv0.Component{Name: "redis", Type: agentv0.ComponentType_MODULE, BomRef: "root"}
	base, err := finish(root, []*agentv0.Component{{Name: "openssl", BomRef: "pkg:deb/openssl"}}, nil, "syft", "DOCKER")
	require.NoError(t, err)

	first, err := bindImageDigest(base, "sha256:abcdef")
	require.NoError(t, err)
	second, err := bindImageDigest(base, "sha256:abcdef")
	require.NoError(t, err)

	require.Empty(t, base.Bom.GetMetadata().GetComponent().GetHashes(), "input document was mutated")
	require.Equal(t, agentv0.ComponentType_MODULE, base.Bom.GetMetadata().GetComponent().GetType())
	require.Len(t, first.Bom.GetMetadata().GetComponent().GetHashes(), 1)
	require.Equal(t, first.SHA256, second.SHA256)
}

func TestManagedSyftArgsForKeepHardeningOnEveryTarget(t *testing.T) {
	args := strings.Join(managedSyftArgsFor("registry:redis@sha256:abc"), " ")
	require.Contains(t, args, "--read-only")
	require.Contains(t, args, "--cap-drop ALL")
	require.Contains(t, args, "--security-opt no-new-privileges")
	require.Contains(t, args, "registry:redis@sha256:abc")
	require.NotContains(t, args, "/var/run/docker.sock")
}

// A subject written before the selector existed carries no kind at all, and it
// must keep resolving through the registry rather than probing the daemon: a
// stale local image answering to the same tag would bind evidence to an image
// nothing deployed.
func TestSourceOfDefaultsToRegistry(t *testing.T) {
	require.Equal(t, SourceRegistry, SourceOf(&builderv0.ImageSubject{Reference: "ghcr.io/codefly-dev/app:1.0"}))
	require.Equal(t, SourceRegistry, SourceOf(&builderv0.ImageSubject{Source: builderv0.ImageSourceKind_IMAGE_SOURCE_KIND_REGISTRY}))
	require.Equal(t, SourceDockerDaemon, SourceOf(&builderv0.ImageSubject{Source: builderv0.ImageSourceKind_IMAGE_SOURCE_KIND_DOCKER_DAEMON}))

	require.Equal(t, builderv0.ImageSourceKind_IMAGE_SOURCE_KIND_REGISTRY, SourceKind(SourceRegistry))
	require.Equal(t, builderv0.ImageSourceKind_IMAGE_SOURCE_KIND_DOCKER_DAEMON, SourceKind(SourceDockerDaemon))
}
