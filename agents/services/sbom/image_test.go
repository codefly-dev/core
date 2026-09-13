package sbom

import (
	"strings"
	"testing"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	"github.com/stretchr/testify/require"
)

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

func TestManagedSyftArgsForKeepHardeningOnEveryTarget(t *testing.T) {
	args := strings.Join(managedSyftArgsFor("registry:redis@sha256:abc"), " ")
	require.Contains(t, args, "--read-only")
	require.Contains(t, args, "--cap-drop ALL")
	require.Contains(t, args, "--security-opt no-new-privileges")
	require.Contains(t, args, "registry:redis@sha256:abc")
	require.NotContains(t, args, "/var/run/docker.sock")
}
