package composition

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
)

type ReleaseMetadata struct {
	Repository string
	Ref        string
	Commit     string
	Manifest   []byte
	Provenance []byte
	Signature  []byte
}

type MetadataResolver interface {
	ResolveMetadata(context.Context, ResolveRequest) (*ReleaseMetadata, error)
}

type verifiedMetadata struct {
	manifest    *PackageManifest
	provenance  *Provenance
	attestation *ReleaseMetadata
}

func verifyMetadata(metadata *ReleaseMetadata, selection ReleaseSelection, trust TrustPolicy) (*verifiedMetadata, error) {
	if err := selection.validate(); err != nil {
		return nil, err
	}
	if metadata == nil {
		return nil, errors.New("signed release metadata is required")
	}
	provenance, err := ParseProvenance(metadata.Provenance)
	if err != nil {
		return nil, err
	}
	release := &Release{Repository: metadata.Repository, Ref: metadata.Ref, Commit: metadata.Commit, Provenance: metadata.Provenance, Signature: metadata.Signature}
	if err := verifyProvenance(release, provenance, selection.ID, selection.Version, trust); err != nil {
		return nil, err
	}
	if provenance.ArtifactDigest != selection.Digest || provenance.ManifestDigest != fmt.Sprintf("sha256:%x", sha256.Sum256(metadata.Manifest)) {
		return nil, fmt.Errorf("%w: selected release or metadata differs from signed provenance", ErrDigestMismatch)
	}
	var manifest PackageManifest
	if err := decodeStrictYAML(metadata.Manifest, &manifest); err != nil {
		return nil, err
	}
	if err := manifest.Validate(); err != nil {
		return nil, err
	}
	if manifest.ID != selection.ID || manifest.Version != selection.Version {
		return nil, ErrPackageIdentity
	}
	copy := *metadata
	copy.Manifest = append([]byte(nil), metadata.Manifest...)
	copy.Provenance = append([]byte(nil), metadata.Provenance...)
	copy.Signature = append([]byte(nil), metadata.Signature...)
	return &verifiedMetadata{manifest: &manifest, provenance: provenance, attestation: &copy}, nil
}
