package resources_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/resources"
)

const lockedImageName = "service-warehouse"

func lock(t *testing.T, fixture string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("testdata", "image-lock", fixture+".json"))
	require.NoError(t, err)
	return content
}

func TestParseImageLockPinsTheDigestInCoresRegistry(t *testing.T) {
	image, err := resources.ParseImageLock(lock(t, "published"), lockedImageName)
	require.NoError(t, err)
	require.Equal(t, resources.ImageRegistry, image.Repository)
	require.Equal(t, lockedImageName, image.Name)
	require.Empty(t, image.Tag, "the lock carries no tag, so a tag cannot disagree with the digest")
	require.Equal(t,
		"ghcr.io/codefly-dev/service-warehouse@sha256:1205da0743e56b68c12c754438476e8fdd0f05cdb08ce1c9687ddb8d8f0e1669",
		image.FullName())
}

// A lock that is accepted but names no immutable image is the failure pinning
// exists to prevent, so every malformed shape is refused at load.
func TestParseImageLockRefusesAnythingButASHA256DigestOfThisImage(t *testing.T) {
	for _, fixture := range []string{
		"no-name", "other-image", "other-registry",
		"tag-as-digest", "other-algorithm", "truncated", "not-hex", "too-long",
		"not-json",
	} {
		t.Run(fixture, func(t *testing.T) {
			image, err := resources.ParseImageLock(lock(t, fixture), lockedImageName)
			require.Error(t, err, "accepted a lock that pins no image")
			require.Nil(t, image)
			require.NotErrorIs(t, err, resources.ErrImageUnpublished,
				"a wrong lock must not read as a merely unpublished one")
		})
	}
}

// A lock with no digest is the tree before its first image was published: a
// reference with no digest and no tag is the registry's :latest.
func TestAnUnpublishedLockIsRefusedNeverRunAsLatest(t *testing.T) {
	image, err := resources.ParseImageLock(lock(t, "unpublished"), lockedImageName)
	require.ErrorIs(t, err, resources.ErrImageUnpublished)
	require.Nil(t, image)
}

func TestIsSHA256Digest(t *testing.T) {
	const hex64 = "1205da0743e56b68c12c754438476e8fdd0f05cdb08ce1c9687ddb8d8f0e1669"
	for digest, want := range map[string]bool{
		"sha256:" + hex64:        true,
		"sha256:" + hex64 + "00": false,
		"sha256:" + hex64[:62]:   false,
		"sha512:" + hex64:        false,
		"SHA256:" + hex64:        false,
		"sha256:":                false,
		hex64:                    false,
		"":                       false,
	} {
		require.Equal(t, want, resources.IsSHA256Digest(digest), "%q", digest)
	}
}
