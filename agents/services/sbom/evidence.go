package sbom

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
)

// LocalImageID reports the identity the local Docker daemon holds an image
// under. It is the only immutable identity an image that was built and never
// pushed has, because the daemon resolves whatever reference it is handed to
// that ID: a pin sitting only in the reference is not what such a scan binds
// to. docker's own message is kept in the error, because an absent image, an
// absent daemon and an absent docker are different things to go fix and only
// that message tells them apart.
func LocalImageID(ctx context.Context, reference string) (string, error) {
	id, _, err := inspectDaemonImage(ctx, ImageRequest{Reference: reference})
	return id, err
}

// ImageSubjects describes the images a service ships, as the subjects to
// inventory. A published image is multi-architecture and contributes one
// subject per shipped platform, derived from its digest-pinned reference
// (ExpectedFromImageReference, which refuses a bare tag). A local image was
// built and never pushed, so the daemon holds exactly one platform of it: the
// subject names none, which is read back from the image rather than asserted
// by the caller, and carries the image ID instead of a pin sitting in the
// reference (see LocalImageID).
func ImageSubjects(ctx context.Context, image PublishedImage, local bool) ([]*builderv0.ImageSubject, error) {
	if !local {
		return ExpectedFromImageReference(image)
	}
	if image.Service == "" {
		return nil, fmt.Errorf("image %s cannot be inventoried for no service", image.Reference)
	}
	id, err := LocalImageID(ctx, image.Reference)
	if err != nil {
		return nil, err
	}
	return []*builderv0.ImageSubject{{
		Reference: image.Reference,
		Digest:    id,
		Role:      image.Role,
		Service:   image.Service,
		Source:    sourceKind(SourceDockerDaemon),
	}}, nil
}

// ImageEvidenceDocument is one image platform's inventory, written to disk and
// named by the digest it is bound to.
type ImageEvidenceDocument struct {
	// Reference is the reference the subject asked for.
	Reference string
	// Digest is the digest actually scanned.
	Digest string
	// Platform is "os/arch", read back from the image where it states one.
	Platform string
	// Path is the CycloneDX file.
	Path string
	// SHA256 is the inventory's content digest, Result.SHA256: the digest of the
	// deterministic encoding of the BOM, which is not the digest of the
	// CycloneDX file's bytes.
	SHA256 string
}

// CollectImageEvidence inventories every subject through Image and writes one
// CycloneDX document each into dir, creating it. The document carries the
// scanned digest in its own root component, so an exported artifact still
// names the image it describes rather than relying on the filename to say so.
//
// A single failed scan fails the whole call: publishing evidence for some
// platforms while silently omitting others is the false coverage claim this
// evidence exists to prevent.
func CollectImageEvidence(ctx context.Context, dir string, subjects []*builderv0.ImageSubject) ([]ImageEvidenceDocument, error) {
	if len(subjects) == 0 {
		return nil, fmt.Errorf("no image subjects to inventory")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create evidence directory: %w", err)
	}
	documents := make([]ImageEvidenceDocument, 0, len(subjects))
	for _, subject := range subjects {
		result, err := Image(ctx, ImageRequest{
			Reference: subject.GetReference(),
			Platform:  subject.GetPlatform(),
			Source:    SourceOf(subject),
		})
		if err != nil {
			return nil, fmt.Errorf("inventory %s: %w", describeSubject(subject), err)
		}
		document, err := writeImageEvidence(dir, result)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", describeSubject(subject), err)
		}
		documents = append(documents, document)
	}
	return documents, nil
}

// writeImageEvidence encodes one scanned image as CycloneDX and writes it.
func writeImageEvidence(dir string, result *ImageResult) (ImageEvidenceDocument, error) {
	encoded, err := MarshalCycloneDXJSON(result.Bom)
	if err != nil {
		return ImageEvidenceDocument{}, fmt.Errorf("encode: %w", err)
	}
	path := filepath.Join(dir, evidenceFileName(result.Reference, result.Platform, result.Digest))
	if err := os.WriteFile(path, encoded, 0o644); err != nil {
		return ImageEvidenceDocument{}, fmt.Errorf("write %s: %w", path, err)
	}
	return ImageEvidenceDocument{
		Reference: result.Reference,
		Digest:    result.Digest,
		Platform:  result.Platform,
		Path:      path,
		SHA256:    result.SHA256,
	}, nil
}

// WriteImageEvidenceIndex records which image and platform each document
// covers in dir/index.txt, so a release asset is readable without opening every
// document. One line per document: platform, repository@digest, file name, and
// the inventory's SHA256. The repository is the reference stripped of any tag, so
// the index names the digest actually scanned rather than the tag asked for.
func WriteImageEvidenceIndex(dir string, documents []ImageEvidenceDocument) error {
	var index strings.Builder
	for _, document := range documents {
		fmt.Fprintf(&index, "%s %s@%s %s %s\n",
			document.Platform, referenceName(document.Reference), document.Digest,
			filepath.Base(document.Path), document.SHA256)
	}
	return os.WriteFile(filepath.Join(dir, "index.txt"), []byte(index.String()), 0o644)
}

// evidenceFileName names a document after the repository it inventories, the
// platform and the digest it is bound to, none of which a caller types:
// "service-warehouse-linux-amd64-<hex>.cdx.json".
func evidenceFileName(reference, platform, digest string) string {
	repository := referenceName(reference)
	name := repository[strings.LastIndex(repository, "/")+1:]
	if platform != "" {
		name += "-" + strings.ReplaceAll(platform, "/", "-")
	}
	return name + "-" + strings.TrimPrefix(digest, "sha256:") + ".cdx.json"
}

func describeSubject(subject *builderv0.ImageSubject) string {
	if platform := subject.GetPlatform(); platform != "" {
		return subject.GetReference() + " on " + platform
	}
	return subject.GetReference()
}
