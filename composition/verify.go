package composition

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
)

type TrustPolicy struct {
	Repositories map[string]string
	Signers      map[string]ed25519.PublicKey
}

func DecodeSignature(encoded []byte) ([]byte, error) {
	decoded, err := base64.StdEncoding.DecodeString(string(bytesTrimSpace(encoded)))
	if err != nil {
		return nil, fmt.Errorf("decode module provenance signature: %w", err)
	}
	if len(decoded) != ed25519.SignatureSize {
		return nil, fmt.Errorf("decode module provenance signature: got %d bytes, want %d", len(decoded), ed25519.SignatureSize)
	}
	return decoded, nil
}

func VerifyRelease(release *Release, expectedPackage, expectedVersion string, trust TrustPolicy) (*VerifiedRelease, error) {
	if release == nil {
		return nil, errors.New("module release is required")
	}
	provenance, err := ParseProvenance(release.Provenance)
	if err != nil {
		return nil, err
	}
	expectedRepository, exists := trust.Repositories[expectedPackage]
	if !exists || expectedRepository == "" || expectedRepository != release.Repository || expectedRepository != provenance.Repository {
		return nil, fmt.Errorf("module release repository %q is not trusted for package %q", release.Repository, expectedPackage)
	}
	publicKey, exists := trust.Signers[provenance.SignatureIdentity]
	signature := release.Signature
	if len(signature) != ed25519.SignatureSize {
		signature, _ = DecodeSignature(signature)
	}
	if !exists || len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(publicKey, release.Provenance, signature) {
		return nil, ErrSignature
	}
	digest := sha256.Sum256(release.Artifact)
	artifactDigest := fmt.Sprintf("sha256:%x", digest)
	if provenance.ArtifactDigest != artifactDigest {
		return nil, fmt.Errorf("%w: got %s, want %s", ErrDigestMismatch, artifactDigest, provenance.ArtifactDigest)
	}
	if provenance.Package != expectedPackage || (expectedVersion != "" && provenance.Version != expectedVersion) {
		return nil, fmt.Errorf("%w: provenance identifies %s@%s", ErrPackageIdentity, provenance.Package, provenance.Version)
	}
	if provenance.Ref != release.Ref || provenance.Commit != release.Commit || provenance.Repository != release.Repository {
		return nil, errors.New("module provenance does not match resolved repository, tag, and peeled commit")
	}
	temporary, err := os.MkdirTemp("", "codefly-verify-module-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	if err := ExtractArchive(context.Background(), release.Artifact, temporary); err != nil {
		return nil, err
	}
	manifest, err := LoadPackageManifest(temporary)
	if err != nil {
		return nil, err
	}
	if manifest.ID != expectedPackage || manifest.Version != provenance.Version {
		return nil, fmt.Errorf("%w: manifest identifies %s@%s", ErrPackageIdentity, manifest.ID, manifest.Version)
	}
	verifiedRelease := &Release{
		Repository: release.Repository,
		Ref:        release.Ref,
		Commit:     release.Commit,
		Artifact:   append([]byte(nil), release.Artifact...),
		Provenance: append([]byte(nil), release.Provenance...),
		Signature:  append([]byte(nil), release.Signature...),
	}
	return &VerifiedRelease{release: verifiedRelease, provenance: provenance, manifest: manifest, digest: artifactDigest}, nil
}

func VerifyLockedRelease(release *Release, lock *Lock, trust TrustPolicy) (*VerifiedRelease, error) {
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	verified, err := VerifyRelease(release, lock.Package, lock.Version, trust)
	if err != nil {
		return nil, err
	}
	if release.Repository != lock.Source.Repository || release.Ref != lock.Source.Ref || release.Commit != lock.Source.Commit ||
		verified.digest != lock.Artifact.Digest || verified.provenance.SignatureIdentity != lock.Artifact.Signature {
		return nil, fmt.Errorf("%w: fetched release does not match locked tag", ErrMovedTag)
	}
	return verified, nil
}
