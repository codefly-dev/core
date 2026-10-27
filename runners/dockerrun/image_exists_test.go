package dockerrun

import (
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/docker/docker/api/types/image"
	"github.com/stretchr/testify/require"
)

// TestImageMatchesReference: a tag is found on RepoTags; a digest reference is
// found on RepoDigests under any repository name, or on the ID of an image that
// was built here and never pushed; and neither form matches the other's image.
func TestImageMatchesReference(t *testing.T) {
	const digest = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
	const other = "sha256:2222222222222222222222222222222222222222222222222222222222222222"
	byTag := &resources.DockerImage{Repository: "ghcr.io/codefly-dev", Name: "documents/docx", Tag: "0.2.0"}
	byDigest := &resources.DockerImage{Repository: "ghcr.io/codefly-dev", Name: "documents/docx", Tag: "0.2.0", Digest: digest}

	tagged := &image.Summary{ID: other, RepoTags: []string{"ghcr.io/codefly-dev/documents/docx:0.2.0"}}
	require.True(t, imageMatchesReference(tagged, byTag))
	require.False(t, imageMatchesReference(tagged, byDigest), "a tag says nothing about the digest")

	pulled := &image.Summary{ID: other, RepoDigests: []string{"ghcr.io/codefly-dev/documents/docx@" + digest}}
	require.True(t, imageMatchesReference(pulled, byDigest))
	require.False(t, imageMatchesReference(pulled, byTag), "a digest-only image carries no tag")

	mirrored := &image.Summary{ID: other, RepoDigests: []string{"mirror.example/docx@" + digest}}
	require.True(t, imageMatchesReference(mirrored, byDigest), "the digest names the bytes, not the repository")

	builtLocally := &image.Summary{ID: digest}
	require.True(t, imageMatchesReference(builtLocally, byDigest), "a local build has no RepoDigests; its ID is its digest")

	different := &image.Summary{ID: other, RepoDigests: []string{"ghcr.io/codefly-dev/documents/docx@" + other}}
	require.False(t, imageMatchesReference(different, byDigest))
}
