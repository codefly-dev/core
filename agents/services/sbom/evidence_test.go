package sbom

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

const (
	digestAMD64 = "sha256:c64c687cbea9300178b30c95835354e34c4e4febc4badfe27102879de0483b5e"
	digestARM64 = "sha256:6243dec9286873e0392f0527f96c394684dc6fa12661f0bd1925abcdef012345"
	pinned      = "ghcr.io/codefly-dev/service-warehouse@sha256:1205da0743e56b68c12c754438476e8fdd0f05cdb08ce1c9687ddb8d8f0e1669"
)

// scanned is what Image returns for a platform of the image: a real bound
// CycloneDX document, built the way Image builds it from a scanner's output.
func scanned(t *testing.T, reference, platform, digest string) *ImageResult {
	t.Helper()
	root := &agentv0.Component{Name: "service-warehouse", Type: agentv0.ComponentType_MODULE, BomRef: "root"}
	packages := []*agentv0.Component{{Name: "openssl", Version: "3.0", BomRef: "pkg:deb/openssl@3.0"}}
	base, err := finish(root, packages, nil, "syft", "DOCKER")
	require.NoError(t, err)
	bound, err := bindImageDigest(base, digest)
	require.NoError(t, err)
	return &ImageResult{Result: bound, Reference: reference, Digest: digest, Platform: platform}
}

// The document on disk is the CycloneDX encoding of the scanned image, carries
// the digest it is bound to in its own root, and is named by repository,
// platform and digest so a directory of them is readable at a glance.
func TestWriteImageEvidenceNamesAndBindsTheDocument(t *testing.T) {
	dir := t.TempDir()
	result := scanned(t, pinned, "linux/amd64", digestAMD64)
	document, err := writeImageEvidence(dir, result)
	require.NoError(t, err)

	require.Equal(t, filepath.Join(dir,
		"service-warehouse-linux-amd64-c64c687cbea9300178b30c95835354e34c4e4febc4badfe27102879de0483b5e.cdx.json"), document.Path)
	require.Equal(t, pinned, document.Reference)
	require.Equal(t, digestAMD64, document.Digest)
	require.Equal(t, "linux/amd64", document.Platform)

	onDisk, err := os.ReadFile(document.Path)
	require.NoError(t, err)
	require.Contains(t, string(onDisk), strings.TrimPrefix(digestAMD64, "sha256:"),
		"the document must name the image it describes itself, not only through its file name")
	require.True(t, json.Valid(onDisk), "document is valid JSON")
	require.Contains(t, string(onDisk), `"bomFormat": "CycloneDX"`)
	require.Equal(t, result.SHA256, document.SHA256, "the index carries the inventory's own digest")
}

func TestEvidenceFileName(t *testing.T) {
	for _, tc := range []struct{ reference, platform, digest, want string }{
		{pinned, "linux/arm64", "sha256:abc", "service-warehouse-linux-arm64-abc.cdx.json"},
		{"localhost:5000/team/app:dev", "linux/amd64", "sha256:abc", "app-linux-amd64-abc.cdx.json"},
		{"redis:7", "linux/amd64", "sha256:abc", "redis-linux-amd64-abc.cdx.json"},
		{pinned, "", "sha256:abc", "service-warehouse-abc.cdx.json"},
	} {
		require.Equal(t, tc.want, evidenceFileName(tc.reference, tc.platform, tc.digest), tc.reference)
	}
}

func TestWriteImageEvidenceIndexListsEveryDocument(t *testing.T) {
	dir := t.TempDir()
	var documents []ImageEvidenceDocument
	for _, platform := range []struct{ name, digest string }{{"linux/amd64", digestAMD64}, {"linux/arm64", digestARM64}} {
		document, err := writeImageEvidence(dir, scanned(t, "ghcr.io/codefly-dev/service-warehouse:0.1.0", platform.name, platform.digest))
		require.NoError(t, err)
		documents = append(documents, document)
	}
	require.NoError(t, WriteImageEvidenceIndex(dir, documents))

	index, err := os.ReadFile(filepath.Join(dir, "index.txt"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(index)), "\n")
	require.Len(t, lines, 2)
	for i, document := range documents {
		// The tag that was asked for is replaced by the digest that was scanned.
		want := strings.Join([]string{
			document.Platform,
			"ghcr.io/codefly-dev/service-warehouse@" + document.Digest,
			filepath.Base(document.Path),
			document.SHA256,
		}, " ")
		require.Equal(t, want, lines[i])
	}
}

func TestCollectImageEvidenceRefusesNoSubjects(t *testing.T) {
	_, err := CollectImageEvidence(context.Background(), t.TempDir(), nil)
	require.ErrorContains(t, err, "no image subjects")
}

// Evidence for some platforms with the rest silently omitted is a false
// coverage claim, so a subject that cannot be scanned fails the whole call and
// names the subject. An empty reference cannot be scanned on any machine, with
// or without a scanner installed.
func TestCollectImageEvidenceFailsWholeOnOneUnscannableSubject(t *testing.T) {
	dir := t.TempDir()
	_, err := CollectImageEvidence(context.Background(), dir, []*builderv0.ImageSubject{
		{Reference: "", Platform: "linux/arm64"},
	})
	require.ErrorContains(t, err, "inventory  on linux/arm64")
	require.ErrorContains(t, err, "image SBOM requires an image reference")
}

func TestImageSubjectsForAPublishedImageIsOneSubjectPerPlatform(t *testing.T) {
	subjects, err := ImageSubjects(context.Background(), PublishedImage{
		Service:   "codefly.dev/warehouse",
		Role:      "runtime",
		Reference: pinned,
		Platforms: []string{"linux/amd64", "linux/arm64"},
	}, false)
	require.NoError(t, err)
	require.Len(t, subjects, 2)
	for i, platform := range []string{"linux/amd64", "linux/arm64"} {
		require.Equal(t, platform, subjects[i].GetPlatform())
		require.Equal(t, pinned, subjects[i].GetReference())
		require.Equal(t, "runtime", subjects[i].GetRole())
		require.Equal(t, "codefly.dev/warehouse", subjects[i].GetService())
		require.Equal(t, SourceRegistry, SourceOf(subjects[i]))
	}
}

// A tag serves whatever was pushed to it last, so a published image that is
// not pinned is refused rather than inventoried as if it were the shipped one.
func TestImageSubjectsRefuseAPublishedImageThatIsNotPinned(t *testing.T) {
	_, err := ImageSubjects(context.Background(), PublishedImage{
		Service: "codefly.dev/warehouse", Role: "runtime",
		Reference: "ghcr.io/codefly-dev/service-warehouse:0.1.0",
	}, false)
	require.ErrorContains(t, err, "not pinned to a sha256 digest")
}

func TestImageSubjectsRefuseALocalImageForNoService(t *testing.T) {
	_, err := ImageSubjects(context.Background(), PublishedImage{Reference: "service-warehouse:local"}, true)
	require.ErrorContains(t, err, "no service")
}
