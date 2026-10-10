//go:build image_evidence_required

package sbom

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This uses real, locally built scratch images and the installed syft, with no
// registry pulls. Run with -tags=image_evidence_required; missing tools fail.
func TestLocalImageEvidenceRemainsBoundWhenATagMoves(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	_, err := exec.LookPath("syft")
	require.NoError(t, err)
	name := "codefly-evidence-" + strconv.FormatInt(time.Now().UnixNano(), 10) + ":test"
	build := func(fixture string) string {
		t.Helper()
		out, err := exec.CommandContext(ctx, "docker", "build", "--quiet", "--tag", name+"-"+fixture,
			"-f", "testdata/image-evidence/Dockerfile", "testdata/image-evidence/"+fixture).CombinedOutput()
		require.NoError(t, err, "%s", out)
		out, err = exec.CommandContext(ctx, "docker", "tag", name+"-"+fixture, name).CombinedOutput()
		require.NoError(t, err, "%s", out)
		id, err := LocalImageID(ctx, name)
		require.NoError(t, err)
		t.Cleanup(func() {
			out, err := exec.Command("docker", "image", "rm", "--force", id).CombinedOutput()
			require.NoError(t, err, "%s", out)
		})
		return id
	}
	first := build("first")
	subjects, err := ImageSubjects(ctx, PublishedImage{Service: "evidence-fixture", Reference: name}, true)
	require.NoError(t, err)
	require.Equal(t, first, subjects[0].GetDigest())

	dir := t.TempDir()
	documents, err := CollectImageEvidence(ctx, dir, subjects)
	require.NoError(t, err)
	require.Len(t, documents, 1)
	require.Equal(t, first, documents[0].Digest)
	require.NotEmpty(t, documents[0].Platform)
	raw, err := os.ReadFile(documents[0].Path)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"version": "1.0.0"`)
	require.NoError(t, WriteImageEvidenceIndex(dir, documents))

	second := build("second")
	require.NotEqual(t, first, second)
	refusedDir := t.TempDir()
	documents, err = CollectImageEvidence(ctx, refusedDir, subjects)
	require.ErrorContains(t, err, "not the requested "+first)
	require.Nil(t, documents)
	entries, err := os.ReadDir(refusedDir)
	require.NoError(t, err)
	require.Empty(t, entries, "a changed tag must publish no evidence for the original subject")

	// Simulate the real interval between inspect and scan: the tag now holds
	// the second image, but the scanner must read the first image's bytes.
	result, err := scanImage(ctx, ImageRequest{Reference: name, Source: SourceDockerDaemon}, first)
	require.NoError(t, err)
	var versions []string
	for _, component := range result.Bom.GetComponents() {
		if component.GetName() == "evidence-fixture" {
			versions = append(versions, component.GetVersion())
		}
	}
	require.Equal(t, []string{"1.0.0"}, versions)
}
