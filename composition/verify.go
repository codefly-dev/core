package composition

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	updatev0 "github.com/codefly-dev/core/generated/go/codefly/update/v0"
	"github.com/codefly-dev/core/moduleupdate"
	"google.golang.org/protobuf/proto"
)

type TrustPolicy struct {
	Repositories map[string]string
	Signers      map[string]map[string]ed25519.PublicKey
	BuildSigners map[string]map[string]ed25519.PublicKey
}

func (release *VerifiedRelease) ContractSnapshot(ctx context.Context) (*updatev0.ContractSnapshot, error) {
	evidence, err := release.ContractEvidence(ctx)
	if err != nil {
		return nil, err
	}
	return evidence.Snapshot, nil
}

func (release *VerifiedRelease) ContractEvidence(ctx context.Context) (*moduleupdate.ContractEvidence, error) {
	if release == nil || release.release == nil {
		return nil, errors.New("verified module release is required")
	}
	root, err := os.MkdirTemp("", "codefly-contract-snapshot-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	if err := ExtractArchive(ctx, release.release.Artifact, root); err != nil {
		return nil, err
	}
	return BuildPackageContractEvidence(root)
}

// ContractDiff reads update evidence from the authenticated module archive.
// A digest in an unattested sidecar is not proof that a release contains it.
func (release *VerifiedRelease) ContractDiff() (*updatev0.ReleaseDiff, error) {
	if err := release.prepareUpdate(); err != nil {
		return nil, err
	}
	return proto.Clone(release.updateDiff).(*updatev0.ReleaseDiff), nil
}

func (release *VerifiedRelease) prepareUpdate() error {
	if release == nil || release.release == nil {
		return errors.New("verified module release is required")
	}
	release.updateMu.Lock()
	defer release.updateMu.Unlock()
	if release.update != nil {
		return nil
	}
	diff, err := release.readContractDiff()
	if err != nil {
		return err
	}
	prepared, err := moduleupdate.PrepareReleaseDiff(diff)
	if err != nil {
		return err
	}
	release.updateDiff, release.update = diff, prepared
	return nil
}

func (release *VerifiedRelease) readContractDiff() (*updatev0.ReleaseDiff, error) {
	root, err := os.MkdirTemp("", "codefly-update-evidence-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(root) }()
	if err := ExtractArchive(context.Background(), release.release.Artifact, root); err != nil {
		return nil, err
	}
	data, err := os.ReadFile(filepath.Join(root, moduleupdate.ReleaseDiffFileName))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("module release is missing %s", moduleupdate.ReleaseDiffFileName)
	}
	if err != nil {
		return nil, err
	}
	diff, err := moduleupdate.ParseReleaseDiff(data)
	if err != nil {
		return nil, err
	}
	if diff.After.Module != release.manifest.ID || diff.After.Version != release.manifest.Version {
		return nil, fmt.Errorf("%w: contract diff does not describe the verified module release", ErrPackageIdentity)
	}
	actual, err := BuildPackageContractSnapshot(root)
	if err != nil {
		return nil, fmt.Errorf("derive packaged contracts: %w", err)
	}
	expected, err := moduleupdate.PrepareSnapshot(diff.After)
	if err != nil {
		return nil, err
	}
	if !proto.Equal(actual, expected) {
		return nil, fmt.Errorf("candidate contract snapshot does not match packaged contract sources")
	}
	return diff, nil
}

func (release *VerifiedRelease) EvaluateUpdate(pin *updatev0.ConsumerPin) *updatev0.UpdateResult {
	err := release.prepareUpdate()
	if err != nil {
		result := &updatev0.UpdateResult{
			Consumer: pin.GetConsumer(), Module: pin.GetModule(), FromVersion: pin.GetVersion(),
			Verdict:      updatev0.Verdict_VERDICT_UNDETERMINED,
			Undetermined: []*updatev0.AffectedItem{{Reason: "could not determine: " + err.Error()}},
		}
		if release != nil && release.manifest != nil {
			result.ToVersion = release.manifest.Version
		}
		return result
	}
	return release.update.Evaluate(pin)
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
	if err := verifyProvenance(release, provenance, expectedPackage, expectedVersion, trust); err != nil {
		return nil, err
	}
	digest := sha256.Sum256(release.Artifact)
	artifactDigest := fmt.Sprintf("sha256:%x", digest)
	if provenance.ArtifactDigest != artifactDigest {
		return nil, fmt.Errorf("%w: got %s, want %s", ErrDigestMismatch, artifactDigest, provenance.ArtifactDigest)
	}
	temporary, err := os.MkdirTemp("", "codefly-verify-module-*")
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(temporary) }()
	if err := ExtractArchive(context.Background(), release.Artifact, temporary); err != nil {
		return nil, err
	}
	if provenance.ManifestDigest != "" {
		data, err := os.ReadFile(filepath.Join(temporary, PackageManifestFileName))
		if err != nil {
			return nil, err
		}
		if fmt.Sprintf("sha256:%x", sha256.Sum256(data)) != provenance.ManifestDigest {
			return nil, fmt.Errorf("%w: packaged manifest differs from signed metadata", ErrDigestMismatch)
		}
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

func verifyProvenance(release *Release, provenance *Provenance, expectedPackage, expectedVersion string, trust TrustPolicy) error {
	expectedRepository, exists := trust.Repositories[expectedPackage]
	if !exists || expectedRepository == "" || expectedRepository != release.Repository || expectedRepository != provenance.Repository {
		return fmt.Errorf("module release repository %q is not trusted for package %q", release.Repository, expectedPackage)
	}
	publicKey, exists := trust.Signers[expectedPackage][provenance.SignatureIdentity]
	signature := release.Signature
	if len(signature) != ed25519.SignatureSize {
		signature, _ = DecodeSignature(signature)
	}
	if !exists || len(publicKey) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(publicKey, release.Provenance, signature) {
		return ErrSignature
	}
	if provenance.Package != expectedPackage || (expectedVersion != "" && provenance.Version != expectedVersion) {
		return fmt.Errorf("%w: provenance identifies %s@%s", ErrPackageIdentity, provenance.Package, provenance.Version)
	}
	if provenance.Ref != release.Ref || provenance.Commit != release.Commit || provenance.Repository != release.Repository {
		return errors.New("module provenance does not match resolved repository, tag, and peeled commit")
	}
	return nil
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
