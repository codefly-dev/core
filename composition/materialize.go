package composition

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gofrs/flock"
)

const cacheMarkerName = ".codefly-cache.json"

type Materializer struct {
	Root             string
	MaxEntries       int
	MaxExpandedBytes int64
}

type cacheMarker struct {
	Schema            string `json:"schema"`
	Package           string `json:"package"`
	Version           string `json:"version"`
	ArtifactDigest    string `json:"artifactDigest"`
	SignatureIdentity string `json:"signatureIdentity"`
}

func NewMaterializer(projectRoot string) *Materializer {
	return &Materializer{
		Root:             filepath.Join(projectRoot, ".codefly", "cache", "modules"),
		MaxEntries:       defaultMaxArchiveEntries,
		MaxExpandedBytes: defaultMaxExpandedBytes,
	}
}

func (materializer *Materializer) CachePath(digest string) (string, error) {
	if _, err := digestBytes(digest); err != nil {
		return "", err
	}
	return filepath.Join(materializer.Root, strings.TrimPrefix(digest, "sha256:")), nil
}

func (materializer *Materializer) Materialize(ctx context.Context, release *VerifiedRelease) (string, error) {
	if release == nil || release.Release == nil || release.Manifest == nil || release.Provenance == nil {
		return "", errors.New("verified module release is required")
	}
	target, err := materializer.CachePath(release.Digest)
	if err != nil {
		return "", err
	}
	expected := &cacheMarker{
		Schema:            "codefly/module-cache/v2",
		Package:           release.Manifest.ID,
		Version:           release.Manifest.Version,
		ArtifactDigest:    release.Digest,
		SignatureIdentity: release.Provenance.SignatureIdentity,
	}
	return materializer.withArtifactLock(ctx, release.Digest, func() (string, error) {
		if err := materializer.removeInterrupted(release.Digest); err != nil {
			return "", err
		}
		if err := materializer.verifyCache(target, expected); err == nil {
			return target, nil
		}
		if err := removeCacheTree(target); err != nil {
			return "", fmt.Errorf("evict invalid module cache: %w", err)
		}
		temporary, err := os.MkdirTemp(materializer.Root, ".tmp-"+strings.TrimPrefix(release.Digest, "sha256:")+"-")
		if err != nil {
			return "", fmt.Errorf("create module cache staging directory: %w", err)
		}
		defer func() { _ = removeCacheTree(temporary) }()
		if err := extractArchive(ctx, release.Release.Artifact, temporary, materializer.maxEntries(), materializer.maxExpandedBytes()); err != nil {
			return "", err
		}
		manifest, err := LoadPackageManifest(temporary)
		if err != nil {
			return "", err
		}
		if manifest.ID != expected.Package || manifest.Version != expected.Version {
			return "", ErrPackageIdentity
		}
		canonicalDigest, err := canonicalCacheDigest(temporary)
		if err != nil {
			return "", err
		}
		if canonicalDigest != release.Digest {
			return "", fmt.Errorf("%w: release archive is not canonical", ErrDigestMismatch)
		}
		marker, err := json.Marshal(expected)
		if err != nil {
			return "", err
		}
		if err := os.WriteFile(filepath.Join(temporary, cacheMarkerName), marker, 0o444); err != nil {
			return "", err
		}
		if err := makeReadOnly(temporary); err != nil {
			return "", err
		}
		if err := os.Rename(temporary, target); err != nil {
			if verifyErr := materializer.verifyCache(target, expected); verifyErr == nil {
				return target, nil
			}
			return "", fmt.Errorf("promote module cache: %w", err)
		}
		return target, nil
	})
}

func (materializer *Materializer) Cached(lock *Lock) (string, error) {
	if err := lock.Validate(); err != nil {
		return "", err
	}
	target, err := materializer.CachePath(lock.Artifact.Digest)
	if err != nil {
		return "", err
	}
	expected := &cacheMarker{
		Schema:            "codefly/module-cache/v2",
		Package:           lock.Package,
		Version:           lock.Version,
		ArtifactDigest:    lock.Artifact.Digest,
		SignatureIdentity: lock.Artifact.Signature,
	}
	if err := materializer.verifyCache(target, expected); err != nil {
		return "", err
	}
	return target, nil
}

func (materializer *Materializer) verifyCache(target string, expected *cacheMarker) error {
	markerData, err := os.ReadFile(filepath.Join(target, cacheMarkerName))
	if err != nil {
		return fmt.Errorf("%w: read marker: %v", ErrCacheVerification, err)
	}
	var actual cacheMarker
	if err := decodeStrictJSON(markerData, &actual); err != nil {
		return fmt.Errorf("%w: decode marker: %v", ErrCacheVerification, err)
	}
	if actual.Schema != expected.Schema || actual.Package != expected.Package || actual.Version != expected.Version ||
		actual.ArtifactDigest != expected.ArtifactDigest || actual.SignatureIdentity != expected.SignatureIdentity {
		return fmt.Errorf("%w: marker does not match resolved release", ErrCacheVerification)
	}
	digest, err := canonicalCacheDigest(target)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrCacheVerification, err)
	}
	if digest != expected.ArtifactDigest {
		return fmt.Errorf("%w: canonical artifact digest mismatch", ErrCacheVerification)
	}
	return nil
}

func (materializer *Materializer) withArtifactLock(ctx context.Context, digest string, operation func() (string, error)) (string, error) {
	if err := os.MkdirAll(filepath.Join(materializer.Root, ".locks"), 0o755); err != nil {
		return "", err
	}
	lock := flock.New(filepath.Join(materializer.Root, ".locks", strings.TrimPrefix(digest, "sha256:")+".lock"), flock.SetPermissions(0o600))
	locked, err := lock.TryLockContext(ctx, 10*time.Millisecond)
	if err != nil {
		_ = lock.Close()
		return "", err
	}
	if !locked {
		_ = lock.Close()
		return "", errors.New("module cache lock was not acquired")
	}
	defer func() {
		_ = lock.Unlock()
		_ = lock.Close()
	}()
	return operation()
}

func (materializer *Materializer) removeInterrupted(digest string) error {
	entries, err := os.ReadDir(materializer.Root)
	if err != nil {
		return err
	}
	prefix := ".tmp-" + strings.TrimPrefix(digest, "sha256:") + "-"
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), prefix) {
			if err := removeCacheTree(filepath.Join(materializer.Root, entry.Name())); err != nil {
				return fmt.Errorf("remove interrupted module materialization: %w", err)
			}
		}
	}
	return nil
}

func (materializer *Materializer) maxEntries() int {
	if materializer.MaxEntries < 1 {
		return defaultMaxArchiveEntries
	}
	return materializer.MaxEntries
}

func (materializer *Materializer) maxExpandedBytes() int64 {
	if materializer.MaxExpandedBytes < 1 {
		return defaultMaxExpandedBytes
	}
	return materializer.MaxExpandedBytes
}

func makeReadOnly(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, current)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0o444)
		if info.Mode().Perm()&0o111 != 0 {
			mode = 0o555
		}
		return os.Chmod(current, mode)
	})
	if err != nil {
		return err
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := os.Chmod(directories[index], 0o555); err != nil {
			return err
		}
	}
	return nil
}

func removeCacheTree(target string) error {
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return os.Remove(target)
	}
	_ = filepath.WalkDir(target, func(current string, entry fs.DirEntry, walkErr error) error {
		if walkErr == nil && entry.IsDir() {
			_ = os.Chmod(current, 0o755)
		}
		return nil
	})
	return os.RemoveAll(target)
}
