package artifact

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/Masterminds/semver/v3"
	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/codefly-dev/core/resources"
)

const (
	DescriptorFileName        = "provider.artifact.json"
	DescriptorSchemaV0        = "codefly.provider-artifact/v0"
	InstalledManifestSuffix   = ".provider.codefly.yaml"
	InstalledDescriptorSuffix = ".provider.artifact.json"
)

var digestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)

type Descriptor struct {
	SchemaVersion         string `json:"schema_version"`
	ProtocolVersion       string `json:"protocol_version"`
	ManifestSchemaVersion string `json:"manifest_schema_version"`
	Publisher             string `json:"publisher"`
	Name                  string `json:"name"`
	Version               string `json:"version"`
	BinaryPath            string `json:"binary_path"`
	BinaryDigest          string `json:"binary_digest"`
	ManifestPath          string `json:"manifest_path"`
	ManifestDigest        string `json:"manifest_digest"`
	ArtifactDigest        string `json:"artifact_digest"`
}

type Verified struct {
	Descriptor *Descriptor
	Manifest   *manifest.Manifest
	BinaryPath string
}

func (v *Verified) AdmitRuntimeCatalog(runtime *providerv0.RuntimeCatalog) (*manifest.Catalog, error) {
	if v == nil || v.Manifest == nil || v.Descriptor == nil {
		return nil, fmt.Errorf("verified provider artifact is required")
	}
	catalog, err := v.Manifest.AdmitRuntimeCatalog(runtime)
	if err != nil {
		return nil, err
	}
	if runtime.GetProtocolVersion() != v.Descriptor.ProtocolVersion ||
		runtime.GetManifestSchemaVersion() != v.Descriptor.ManifestSchemaVersion {
		return nil, fmt.Errorf("runtime catalog versions do not match verified artifact")
	}
	return catalog, nil
}

func BuildDescriptor(binaryName string, binary, manifestBytes []byte) (*Descriptor, error) {
	if filepath.Base(binaryName) != binaryName || binaryName == "." || binaryName == "" {
		return nil, fmt.Errorf("binary name must be a leaf artifact path")
	}
	providerManifest, err := manifest.Load(manifestBytes)
	if err != nil {
		return nil, err
	}
	registration, err := resources.AgentKindRegistrationFor(providerManifest.Agent.Kind)
	if err != nil {
		return nil, err
	}
	if binaryName != registration.ExecutableName(providerManifest.Agent.Name) {
		return nil, fmt.Errorf("binary name %q does not match manifest agent %q", binaryName, providerManifest.Agent.Name)
	}
	manifestDigest, err := providerManifest.Digest()
	if err != nil {
		return nil, err
	}
	descriptor := &Descriptor{
		SchemaVersion:         DescriptorSchemaV0,
		ProtocolVersion:       providerManifest.ProtocolVersion,
		ManifestSchemaVersion: providerManifest.SchemaVersion,
		Publisher:             providerManifest.Agent.Publisher,
		Name:                  providerManifest.Agent.Name,
		Version:               providerManifest.Agent.Version,
		BinaryPath:            binaryName,
		BinaryDigest:          digest(binary),
		ManifestPath:          manifest.FileName,
		ManifestDigest:        manifestDigest,
	}
	artifactDigest, err := descriptorDigest(descriptor)
	if err != nil {
		return nil, err
	}
	descriptor.ArtifactDigest = artifactDigest
	return descriptor, nil
}

func MarshalDescriptor(descriptor *Descriptor) ([]byte, error) {
	if err := validateDescriptor(descriptor, true); err != nil {
		return nil, err
	}
	return json.Marshal(descriptor)
}

func ParseDescriptor(data []byte) (*Descriptor, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var descriptor Descriptor
	if err := decoder.Decode(&descriptor); err != nil {
		return nil, fmt.Errorf("decode provider artifact descriptor: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return nil, fmt.Errorf("decode provider artifact descriptor: trailing JSON value")
		}
		return nil, fmt.Errorf("decode provider artifact descriptor: %w", err)
	}
	if err := validateDescriptor(&descriptor, true); err != nil {
		return nil, err
	}
	expected, err := descriptorDigest(&descriptor)
	if err != nil {
		return nil, err
	}
	if descriptor.ArtifactDigest != expected {
		return nil, fmt.Errorf("provider artifact digest mismatch: got %s, want %s", descriptor.ArtifactDigest, expected)
	}
	return &descriptor, nil
}

func VerifyLayout(root string, expected *resources.Agent) (*Verified, error) {
	descriptorBytes, err := os.ReadFile(filepath.Join(root, DescriptorFileName))
	if err != nil {
		return nil, fmt.Errorf("read provider artifact descriptor: %w", err)
	}
	descriptor, err := ParseDescriptor(descriptorBytes)
	if err != nil {
		return nil, err
	}
	if filepath.Base(descriptor.BinaryPath) != descriptor.BinaryPath || filepath.Base(descriptor.ManifestPath) != descriptor.ManifestPath {
		return nil, fmt.Errorf("provider artifact paths must be leaf names")
	}
	return verifyFiles(
		filepath.Join(root, descriptor.BinaryPath),
		filepath.Join(root, descriptor.ManifestPath),
		descriptor,
		expected,
	)
}

func VerifyExecutable(binaryPath string, expected *resources.Agent) (*Verified, error) {
	descriptorPath := binaryPath + InstalledDescriptorSuffix
	manifestPath := binaryPath + InstalledManifestSuffix
	descriptorBytes, err := os.ReadFile(descriptorPath)
	if err != nil {
		roots := []string{filepath.Dir(binaryPath)}
		if filepath.Base(filepath.Dir(binaryPath)) == "bin" {
			roots = append(roots, filepath.Dir(filepath.Dir(binaryPath)))
		}
		for _, root := range roots {
			if _, statErr := os.Stat(filepath.Join(root, DescriptorFileName)); statErr != nil {
				continue
			}
			verified, verifyErr := VerifyLayout(root, expected)
			if verifyErr != nil {
				return nil, verifyErr
			}
			executable, pathErr := filepath.Abs(binaryPath)
			if pathErr != nil {
				return nil, pathErr
			}
			bound, pathErr := filepath.Abs(verified.BinaryPath)
			if pathErr != nil {
				return nil, pathErr
			}
			if filepath.Clean(executable) != filepath.Clean(bound) {
				return nil, fmt.Errorf("provider artifact descriptor binds %s, not executable %s", verified.BinaryPath, binaryPath)
			}
			return verified, nil
		}
		return nil, fmt.Errorf("read installed provider artifact descriptor: %w", err)
	}
	descriptor, err := ParseDescriptor(descriptorBytes)
	if err != nil {
		return nil, err
	}
	return verifyFiles(binaryPath, manifestPath, descriptor, expected)
}

func InstallLayout(root, target string, expected *resources.Agent) (*Verified, error) {
	verified, err := VerifyLayout(root, expected)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return nil, err
	}
	binary, err := os.ReadFile(verified.BinaryPath)
	if err != nil {
		return nil, err
	}
	manifestBytes, err := os.ReadFile(filepath.Join(root, verified.Descriptor.ManifestPath))
	if err != nil {
		return nil, err
	}
	descriptorBytes, err := os.ReadFile(filepath.Join(root, DescriptorFileName))
	if err != nil {
		return nil, err
	}
	for path, data := range map[string][]byte{
		target:                             binary,
		target + InstalledManifestSuffix:   manifestBytes,
		target + InstalledDescriptorSuffix: descriptorBytes,
	} {
		mode := os.FileMode(0o600)
		if path == target {
			mode = 0o755
		}
		if err := writeAtomic(path, data, mode); err != nil {
			return nil, err
		}
	}
	return VerifyExecutable(target, expected)
}

func AdmitVersionTransition(current, next *Descriptor, currentStateVersion uint32) error {
	if err := validateDescriptor(current, true); err != nil {
		return fmt.Errorf("current artifact: %w", err)
	}
	if err := validateDescriptor(next, true); err != nil {
		return fmt.Errorf("next artifact: %w", err)
	}
	if current.Publisher != next.Publisher || current.Name != next.Name {
		return fmt.Errorf("provider artifact identity cannot change during upgrade")
	}
	currentVersion, err := semver.StrictNewVersion(current.Version)
	if err != nil {
		return fmt.Errorf("current provider version: %w", err)
	}
	nextVersion, err := semver.StrictNewVersion(next.Version)
	if err != nil {
		return fmt.Errorf("next provider version: %w", err)
	}
	if nextVersion.LessThan(currentVersion) {
		return fmt.Errorf("provider artifact downgrade from %s to %s is not admitted", current.Version, next.Version)
	}
	if currentStateVersion > manifest.StateSchemaVersionV1 {
		return fmt.Errorf("provider state version %d is newer than supported version %d", currentStateVersion, manifest.StateSchemaVersionV1)
	}
	return nil
}

func verifyFiles(binaryPath, manifestPath string, descriptor *Descriptor, expected *resources.Agent) (*Verified, error) {
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		return nil, fmt.Errorf("read provider binary: %w", err)
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("read provider manifest: %w", err)
	}
	providerManifest, err := manifest.Load(manifestBytes)
	if err != nil {
		return nil, err
	}
	manifestDigest, err := providerManifest.Digest()
	if err != nil {
		return nil, err
	}
	if digest(binary) != descriptor.BinaryDigest {
		return nil, fmt.Errorf("provider binary digest mismatch")
	}
	if manifestDigest != descriptor.ManifestDigest {
		return nil, fmt.Errorf("provider manifest digest mismatch")
	}
	if providerManifest.SchemaVersion != descriptor.ManifestSchemaVersion ||
		providerManifest.ProtocolVersion != descriptor.ProtocolVersion ||
		providerManifest.Agent.Publisher != descriptor.Publisher ||
		providerManifest.Agent.Name != descriptor.Name ||
		providerManifest.Agent.Version != descriptor.Version {
		return nil, fmt.Errorf("provider binary descriptor and manifest identity mismatch")
	}
	if expected != nil {
		if expected.Kind != resources.ProviderAgent || expected.Publisher != descriptor.Publisher ||
			expected.Name != descriptor.Name || expected.Version != descriptor.Version {
			return nil, fmt.Errorf("provider artifact does not match requested agent %s", expected.Identifier())
		}
	}
	return &Verified{Descriptor: descriptor, Manifest: providerManifest, BinaryPath: binaryPath}, nil
}

func validateDescriptor(descriptor *Descriptor, requireDigest bool) error {
	if descriptor == nil {
		return fmt.Errorf("provider artifact descriptor is required")
	}
	if descriptor.SchemaVersion != DescriptorSchemaV0 {
		return fmt.Errorf("unsupported provider artifact schema version %q", descriptor.SchemaVersion)
	}
	if descriptor.ProtocolVersion != manifest.ProtocolVersionV0 || descriptor.ManifestSchemaVersion != manifest.SchemaVersionV0 {
		return fmt.Errorf("provider artifact protocol or manifest schema version is unsupported")
	}
	if descriptor.Publisher == "" || descriptor.Name == "" || descriptor.Version == "" || descriptor.Version == "latest" {
		return fmt.Errorf("provider artifact requires concrete agent identity")
	}
	if filepath.Base(descriptor.BinaryPath) != descriptor.BinaryPath || filepath.Base(descriptor.ManifestPath) != descriptor.ManifestPath {
		return fmt.Errorf("provider artifact paths must be leaf names")
	}
	if descriptor.ManifestPath != manifest.FileName {
		return fmt.Errorf("provider artifact manifest path must be %q", manifest.FileName)
	}
	if !digestPattern.MatchString(descriptor.BinaryDigest) || !digestPattern.MatchString(descriptor.ManifestDigest) {
		return fmt.Errorf("provider artifact file digests must be canonical sha256 values")
	}
	if requireDigest && !digestPattern.MatchString(descriptor.ArtifactDigest) {
		return fmt.Errorf("provider artifact digest must be a canonical sha256 value")
	}
	return nil
}

func descriptorDigest(descriptor *Descriptor) (string, error) {
	clone := *descriptor
	clone.ArtifactDigest = ""
	if err := validateDescriptor(&clone, false); err != nil {
		return "", err
	}
	data, err := json.Marshal(clone)
	if err != nil {
		return "", err
	}
	return digest(data), nil
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".provider-artifact-*")
	if err != nil {
		return err
	}
	tempPath := file.Name()
	defer os.Remove(tempPath)
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		return err
	}
	return nil
}

func IsIntegrityError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "digest mismatch") ||
		strings.Contains(message, "identity mismatch") ||
		strings.Contains(message, "does not match requested agent")
}
