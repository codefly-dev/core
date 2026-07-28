package releaseupdate

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"debug/elf"
	"debug/macho"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	selfupdate "github.com/creativeprojects/go-selfupdate"
)

const (
	defaultMaxArtifactBytes   = 256 << 20
	defaultMaxMetadataBytes   = 4 << 20
	defaultMaxExecutableBytes = 256 << 20
	defaultMaxArchiveBytes    = 512 << 20
)

// InstallerOptions pins release authenticity and download boundaries.
type InstallerOptions struct {
	HTTPClient              *http.Client
	Token                   string
	PublisherCertificatePEM []byte
	ChecksumsAssetName      string
	SignatureAssetName      string
	AllowedDownloadHosts    []string
	MaxArtifactBytes        int64
	MaxMetadataBytes        int64
	MaxExecutableBytes      int64
	MaxArchiveBytes         int64
}

type directInstaller struct {
	client             *http.Client
	token              string
	validator          selfupdate.Validator
	checksumsAssetName string
	signatureAssetName string
	allowedHosts       map[string]struct{}
	maxArtifactBytes   int64
	maxMetadataBytes   int64
	maxExecutableBytes int64
	maxArchiveBytes    int64
}

// NewInstaller constructs a fail-closed direct-binary installer.
func NewInstaller(options InstallerOptions) (Installer, error) {
	block, _ := pem.Decode(options.PublisherCertificatePEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("publisher certificate must contain one PEM certificate")
	}
	certificate, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, errors.New("parse publisher certificate")
	}
	if _, ok := certificate.PublicKey.(*ecdsa.PublicKey); !ok {
		return nil, errors.New("publisher certificate must contain an ECDSA public key")
	}

	if !validAssetName(options.ChecksumsAssetName) {
		return nil, errors.New("checksum asset name must be a plain filename")
	}
	signatureName := options.SignatureAssetName
	if signatureName == "" {
		signatureName = options.ChecksumsAssetName + ".sig"
	}
	if !validAssetName(signatureName) {
		return nil, errors.New("signature asset name must be a plain filename")
	}
	if signatureName == options.ChecksumsAssetName {
		return nil, errors.New("checksum and signature assets must have different names")
	}

	allowedHosts := make(map[string]struct{}, len(options.AllowedDownloadHosts))
	for _, host := range options.AllowedDownloadHosts {
		normalized := strings.ToLower(strings.TrimSpace(host))
		parsed, err := url.Parse("https://" + normalized)
		if normalized == "" || err != nil || parsed.Host == "" || parsed.Host != normalized || parsed.Path != "" || parsed.User != nil {
			return nil, fmt.Errorf("invalid allowed download host %q", host)
		}
		allowedHosts[normalized] = struct{}{}
	}
	if len(allowedHosts) == 0 {
		return nil, errors.New("at least one allowed download host is required")
	}

	client := http.DefaultClient
	if options.HTTPClient != nil {
		client = options.HTTPClient
	}
	clientCopy := *client
	originalRedirect := clientCopy.CheckRedirect
	clientCopy.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return errors.New("download redirect limit exceeded")
		}
		if err := validateDownloadURL(request.URL, allowedHosts); err != nil {
			return err
		}
		if originalRedirect != nil {
			return originalRedirect(request, via)
		}
		return nil
	}

	maxArtifact, err := positiveLimit(options.MaxArtifactBytes, defaultMaxArtifactBytes, "artifact")
	if err != nil {
		return nil, err
	}
	maxMetadata, err := positiveLimit(options.MaxMetadataBytes, defaultMaxMetadataBytes, "metadata")
	if err != nil {
		return nil, err
	}
	maxExecutable, err := positiveLimit(options.MaxExecutableBytes, defaultMaxExecutableBytes, "executable")
	if err != nil {
		return nil, err
	}
	maxArchive, err := positiveLimit(options.MaxArchiveBytes, defaultMaxArchiveBytes, "archive")
	if err != nil {
		return nil, err
	}

	return &directInstaller{
		client:             &clientCopy,
		token:              options.Token,
		validator:          selfupdate.NewChecksumWithECDSAValidator(options.ChecksumsAssetName, options.PublisherCertificatePEM),
		checksumsAssetName: options.ChecksumsAssetName,
		signatureAssetName: signatureName,
		allowedHosts:       allowedHosts,
		maxArtifactBytes:   maxArtifact,
		maxMetadataBytes:   maxMetadata,
		maxExecutableBytes: maxExecutable,
		maxArchiveBytes:    maxArchive,
	}, nil
}

func positiveLimit(configured, fallback int64, label string) (int64, error) {
	if configured == 0 {
		return fallback, nil
	}
	if configured < 1 {
		return 0, fmt.Errorf("%s limit must be positive", label)
	}
	return configured, nil
}

// StageAndVerify authenticates release metadata, validates the artifact
// checksum, constrains extraction, and stages a non-executable replacement.
func (i *directInstaller) StageAndVerify(ctx context.Context, status Status, destination Destination) (*StagedUpdate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateInstallStatus(status, destination); err != nil {
		return nil, err
	}
	release := status.Release
	if release.Asset.Platform != CurrentPlatform() {
		return nil, ErrUnsupportedPlatform
	}

	targetInfo, err := validateDestination(destination)
	if err != nil {
		return nil, err
	}
	if targetInfo.Size() > i.maxExecutableBytes {
		return nil, fmt.Errorf("%w: existing executable", ErrDownloadLimit)
	}
	currentDigest, err := digestFile(ctx, destination.Path)
	if err != nil {
		return nil, fmt.Errorf("hash current executable: %w", err)
	}

	selected, found := publishedAsset(release.Assets, release.Asset)
	if !found {
		return nil, errors.New("selected asset is absent from published release metadata")
	}
	checksums, found := assetNamed(release.Assets, i.checksumsAssetName)
	if !found {
		return nil, fmt.Errorf("%w: checksum manifest", ErrAssetNotFound)
	}
	signature, found := assetNamed(release.Assets, i.signatureAssetName)
	if !found {
		return nil, fmt.Errorf("%w: checksum signature", ErrAssetNotFound)
	}

	checksumData, err := i.downloadBytes(ctx, checksums, i.maxMetadataBytes)
	if err != nil {
		return nil, fmt.Errorf("download checksum manifest: %w", err)
	}
	signatureData, err := i.downloadBytes(ctx, signature, i.maxMetadataBytes)
	if err != nil {
		return nil, fmt.Errorf("download checksum signature: %w", err)
	}
	if err := i.validator.Validate(i.checksumsAssetName, checksumData, signatureData); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAuthenticity, err)
	}

	artifactFile, err := os.CreateTemp(destination.StagingDirectory, ".releaseupdate-artifact-*")
	if err != nil {
		return nil, fmt.Errorf("create artifact staging file: %w", err)
	}
	artifactPath := artifactFile.Name()
	defer func() { _ = os.Remove(artifactPath) }()
	if err := i.downloadFile(ctx, selected, i.maxArtifactBytes, artifactFile); err != nil {
		_ = artifactFile.Close()
		return nil, fmt.Errorf("download release artifact: %w", err)
	}
	if err := artifactFile.Close(); err != nil {
		return nil, fmt.Errorf("close artifact staging file: %w", err)
	}
	// #nosec G304 -- artifactPath was returned by CreateTemp in the validated staging directory.
	artifactData, err := os.ReadFile(artifactPath)
	if err != nil {
		return nil, fmt.Errorf("read artifact staging file: %w", err)
	}
	if err := i.validator.Validate(selected.Name, artifactData, checksumData); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrChecksum, err)
	}
	if err := validateArchive(ctx, artifactData, selected.Name, i.maxArchiveBytes); err != nil {
		return nil, err
	}

	executable, err := selfupdate.DecompressCommand(
		&contextReader{ctx: ctx, reader: bytes.NewReader(artifactData)},
		selected.Name,
		filepath.Base(destination.Path),
		runtime.GOOS,
		runtime.GOARCH,
	)
	if err != nil {
		return nil, fmt.Errorf("extract release executable: %w", err)
	}

	stagedFile, err := os.CreateTemp(destination.StagingDirectory, ".releaseupdate-executable-*")
	if err != nil {
		return nil, fmt.Errorf("create executable staging file: %w", err)
	}
	stagedPath := stagedFile.Name()
	keepStaged := false
	defer func() {
		_ = stagedFile.Close()
		if !keepStaged {
			_ = os.Remove(stagedPath)
		}
	}()

	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(stagedFile, hash), io.LimitReader(&contextReader{ctx: ctx, reader: executable}, i.maxExecutableBytes+1))
	if err != nil {
		return nil, fmt.Errorf("stage release executable: %w", sanitizeURLError(err))
	}
	if written > i.maxExecutableBytes {
		return nil, fmt.Errorf("%w: executable", ErrDownloadLimit)
	}
	if err := stagedFile.Sync(); err != nil {
		return nil, fmt.Errorf("sync staged executable: %w", err)
	}
	if err := stagedFile.Close(); err != nil {
		return nil, fmt.Errorf("close staged executable: %w", err)
	}
	if err := validateExecutablePlatform(stagedPath, release.Asset.Platform); err != nil {
		return nil, err
	}
	if err := os.Chmod(stagedPath, 0o600); err != nil {
		return nil, fmt.Errorf("constrain staged executable permissions: %w", err)
	}

	if err := validateDestinationUnchanged(ctx, destination.Path, targetInfo, currentDigest); err != nil {
		return nil, err
	}
	keepStaged = true
	return &StagedUpdate{
		path:              stagedPath,
		destination:       destination.Path,
		mode:              targetInfo.Mode().Perm(),
		stagedDigest:      hash.Sum(nil),
		destinationDigest: currentDigest,
		destinationInfo:   targetInfo,
		removePath:        os.Remove,
	}, nil
}

func validateInstallStatus(status Status, destination Destination) error {
	if status.InstallKind != InstallKindDirect || destination.InstallKind != InstallKindDirect {
		return ErrInstallNotOwned
	}
	if status.Current.value == "" || status.Latest.value == "" || status.Release.Version.value == "" {
		return ErrUpdateNotAvailable
	}
	if status.Latest.Compare(status.Release.Version) != 0 {
		return ErrUpdateNotAvailable
	}
	if status.Channel != ChannelStable && status.Channel != ChannelBeta {
		return ErrUpdateNotAvailable
	}
	if status.Downgrade != DowngradeDisallow && status.Downgrade != DowngradeAllow {
		return ErrUpdateNotAvailable
	}
	if status.Channel == ChannelStable && (status.Release.Prerelease || status.Release.Version.prerelease()) {
		return ErrUpdateNotAvailable
	}
	comparison := status.Release.Version.Compare(status.Current)
	available := comparison > 0 || (comparison < 0 && status.Downgrade == DowngradeAllow)
	if !available || status.Available != available {
		return ErrUpdateNotAvailable
	}
	return nil
}

func validateDestinationUnchanged(ctx context.Context, path string, original os.FileInfo, digest []byte) error {
	current, err := os.Lstat(path)
	if err != nil ||
		!current.Mode().IsRegular() ||
		current.Mode().Perm() != original.Mode().Perm() ||
		current.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 ||
		!os.SameFile(original, current) {
		return ErrInvalidDestination
	}
	currentDigest, err := digestFile(ctx, path)
	if err != nil || !bytes.Equal(currentDigest, digest) {
		return ErrInvalidDestination
	}
	return nil
}

func validateDestination(destination Destination) (os.FileInfo, error) {
	if !filepath.IsAbs(destination.Path) || filepath.Clean(destination.Path) != destination.Path {
		return nil, ErrInvalidDestination
	}
	if !filepath.IsAbs(destination.StagingDirectory) || filepath.Clean(destination.StagingDirectory) != destination.StagingDirectory {
		return nil, ErrInvalidDestination
	}
	info, err := os.Lstat(destination.Path)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDestination, err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidDestination
	}
	if info.Mode().Perm()&0o111 == 0 || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return nil, ErrInvalidDestination
	}
	stageInfo, err := os.Lstat(destination.StagingDirectory)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidDestination, err)
	}
	if !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 {
		return nil, ErrInvalidDestination
	}
	return info, nil
}

func publishedAsset(assets []Asset, selected Asset) (Asset, bool) {
	for _, candidate := range assets {
		if candidate.ID == selected.ID &&
			candidate.Name == selected.Name &&
			candidate.DownloadURL == selected.DownloadURL &&
			candidate.Size == selected.Size {
			return candidate, true
		}
	}
	return Asset{}, false
}

func assetNamed(assets []Asset, name string) (Asset, bool) {
	for _, asset := range assets {
		if asset.Name == name {
			return asset, true
		}
	}
	return Asset{}, false
}

func validAssetName(name string) bool {
	if name == "" || name != filepath.Base(name) {
		return false
	}
	for _, character := range name {
		if (character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') ||
			strings.ContainsRune("._+-", character) {
			continue
		}
		return false
	}
	return true
}

func (i *directInstaller) downloadBytes(ctx context.Context, asset Asset, limit int64) ([]byte, error) {
	var buffer bytes.Buffer
	if err := i.download(ctx, asset, limit, &buffer); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func (i *directInstaller) downloadFile(ctx context.Context, asset Asset, limit int64, file *os.File) error {
	if err := i.download(ctx, asset, limit, file); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync download: %w", err)
	}
	return nil
}

func (i *directInstaller) download(ctx context.Context, asset Asset, limit int64, destination io.Writer) error {
	if !validAssetName(asset.Name) {
		return errors.New("release asset name is not a plain filename")
	}
	if asset.Size < 0 || asset.Size > limit {
		return ErrDownloadLimit
	}
	downloadURL, err := url.Parse(asset.DownloadURL)
	if err != nil {
		return errors.New("release asset has an invalid download URL")
	}
	if err := validateDownloadURL(downloadURL, i.allowedHosts); err != nil {
		return err
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL.String(), http.NoBody)
	if err != nil {
		return errors.New("create release asset request")
	}
	request.Header.Set("Accept", "application/octet-stream")
	if i.token != "" {
		request.Header.Set("Authorization", "Bearer "+i.token)
	}
	response, err := i.client.Do(request)
	if err != nil {
		return fmt.Errorf("request release asset: %w", sanitizeURLError(err))
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("release asset request returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > limit {
		return ErrDownloadLimit
	}
	written, err := io.Copy(destination, io.LimitReader(response.Body, limit+1))
	if err != nil {
		return fmt.Errorf("read release asset: %w", sanitizeURLError(err))
	}
	if written > limit {
		return ErrDownloadLimit
	}
	if asset.Size > 0 && written != asset.Size {
		return errors.New("release asset length differs from published metadata")
	}
	return nil
}

func validateDownloadURL(downloadURL *url.URL, allowedHosts map[string]struct{}) error {
	if downloadURL == nil ||
		downloadURL.Scheme != "https" ||
		downloadURL.Host == "" ||
		downloadURL.User != nil ||
		downloadURL.Fragment != "" {
		return errors.New("release asset URL must be HTTPS and contain no credentials or fragment")
	}
	if _, ok := allowedHosts[strings.ToLower(downloadURL.Host)]; !ok {
		return errors.New("release asset host is not allowed")
	}
	return nil
}

func validateExecutablePlatform(path string, platform Platform) error {
	switch platform.OS {
	case "linux":
		file, err := elf.Open(path)
		if err != nil {
			return fmt.Errorf("%w: executable is not ELF", ErrUnsupportedPlatform)
		}
		defer func() { _ = file.Close() }()
		expected := map[string]elf.Machine{
			"amd64": elf.EM_X86_64,
			"arm64": elf.EM_AARCH64,
		}[platform.Arch]
		if expected == 0 || file.Machine != expected {
			return ErrUnsupportedPlatform
		}
		return nil

	case "darwin":
		expected := map[string]macho.Cpu{
			"amd64": macho.CpuAmd64,
			"arm64": macho.CpuArm64,
		}[platform.Arch]
		if expected == 0 {
			return ErrUnsupportedPlatform
		}
		if fat, err := macho.OpenFat(path); err == nil {
			defer func() { _ = fat.Close() }()
			for _, architecture := range fat.Arches {
				if architecture.Cpu == expected {
					return nil
				}
			}
			return ErrUnsupportedPlatform
		}
		file, err := macho.Open(path)
		if err != nil {
			return fmt.Errorf("%w: executable is not Mach-O", ErrUnsupportedPlatform)
		}
		defer func() { _ = file.Close() }()
		if file.Cpu != expected {
			return ErrUnsupportedPlatform
		}
		return nil

	default:
		return ErrUnsupportedPlatform
	}
}

func digestFile(ctx context.Context, path string) ([]byte, error) {
	// #nosec G304 -- callers pass an explicitly authorized destination or a private transaction path.
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, &contextReader{ctx: ctx, reader: file}); err != nil {
		return nil, err
	}
	return hash.Sum(nil), nil
}

// StagedUpdate is a verified, single-use replacement.
type StagedUpdate struct {
	mutex             sync.Mutex
	path              string
	destination       string
	mode              os.FileMode
	stagedDigest      []byte
	destinationDigest []byte
	destinationInfo   os.FileInfo
	removePath        func(string) error
	used              bool
}

// Path returns the non-executable staged file path.
func (s *StagedUpdate) Path() string {
	return s.path
}

// Destination returns the explicitly authorized replacement path.
func (s *StagedUpdate) Destination() string {
	return s.destination
}

// Apply atomically replaces the authorized direct binary.
func (s *StagedUpdate) Apply(ctx context.Context) (ApplyResult, error) {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.used {
		return ApplyResult{}, ErrStagedUpdateUsed
	}
	if err := ctx.Err(); err != nil {
		return ApplyResult{}, err
	}

	info, err := os.Lstat(s.destination)
	if err != nil ||
		!info.Mode().IsRegular() ||
		info.Mode().Perm() != s.mode ||
		info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return ApplyResult{}, ErrInvalidDestination
	}
	currentDigest, err := digestFile(ctx, s.destination)
	if err != nil || !bytes.Equal(currentDigest, s.destinationDigest) {
		return ApplyResult{}, ErrInvalidDestination
	}

	staged, err := os.Open(s.path)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("open staged executable: %w", err)
	}
	defer func() { _ = staged.Close() }()

	transactionPath, err := os.MkdirTemp(filepath.Dir(s.destination), ".releaseupdate-transaction-*")
	if err != nil {
		return ApplyResult{}, fmt.Errorf("create replacement transaction: %w", err)
	}
	replacementPath := filepath.Join(transactionPath, "replacement")
	cleanupTransaction := true
	defer func() {
		if cleanupTransaction {
			_ = s.remove(replacementPath)
			_ = s.remove(transactionPath)
		}
	}()

	// #nosec G304 -- replacementPath is inside the private directory created immediately above.
	replacement, err := os.OpenFile(replacementPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("create replacement file: %w", err)
	}
	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(replacement, hash), &contextReader{ctx: ctx, reader: staged}); err != nil {
		_ = replacement.Close()
		return ApplyResult{}, fmt.Errorf("copy staged replacement: %w", err)
	}
	if !bytes.Equal(hash.Sum(nil), s.stagedDigest) {
		_ = replacement.Close()
		return ApplyResult{}, ErrChecksum
	}
	if err := replacement.Chmod(s.mode); err != nil {
		_ = replacement.Close()
		return ApplyResult{}, fmt.Errorf("preserve replacement permissions: %w", err)
	}
	if err := replacement.Sync(); err != nil {
		_ = replacement.Close()
		return ApplyResult{}, fmt.Errorf("sync replacement file: %w", err)
	}
	if err := replacement.Close(); err != nil {
		return ApplyResult{}, fmt.Errorf("close replacement file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return ApplyResult{}, err
	}
	if err := validateDestinationState(ctx, s.destination, s.mode, s.destinationDigest, s.destinationInfo); err != nil {
		return ApplyResult{}, err
	}

	s.used = true
	defer func() { _ = s.remove(s.path) }()

	previousPath := filepath.Join(transactionPath, "previous")
	if err := os.Rename(s.destination, previousPath); err != nil {
		return ApplyResult{}, fmt.Errorf("capture prior executable: %w", err)
	}
	if err := validateDestinationState(context.Background(), previousPath, s.mode, s.destinationDigest, s.destinationInfo); err != nil {
		if restoreErr := restorePrevious(previousPath, s.destination); restoreErr != nil {
			cleanupTransaction = false
			return ApplyResult{PreviousPath: previousPath, CleanupPath: transactionPath},
				fmt.Errorf("%w; restore failed: %w", ErrInvalidDestination, restoreErr)
		}
		return ApplyResult{}, ErrInvalidDestination
	}

	if err := os.Link(replacementPath, s.destination); err != nil {
		if restoreErr := restorePrevious(previousPath, s.destination); restoreErr != nil {
			cleanupTransaction = false
			return ApplyResult{PreviousPath: previousPath, CleanupPath: transactionPath},
				fmt.Errorf("install replacement: %w; restore failed: %w", err, restoreErr)
		}
		return ApplyResult{}, fmt.Errorf("install replacement: %w", err)
	}

	result := ApplyResult{Applied: true}
	if err := s.remove(replacementPath); err != nil {
		cleanupTransaction = false
		result.PreviousPath = previousPath
		result.CleanupPath = transactionPath
		return result, fmt.Errorf("%w: remove replacement link: %w", ErrApplyCleanup, err)
	}
	if err := s.remove(previousPath); err != nil {
		cleanupTransaction = false
		result.PreviousPath = previousPath
		result.CleanupPath = transactionPath
		return result, fmt.Errorf("%w: remove prior executable: %w", ErrApplyCleanup, err)
	}
	if err := s.remove(transactionPath); err != nil {
		cleanupTransaction = false
		result.CleanupPath = transactionPath
		return result, fmt.Errorf("%w: remove transaction directory: %w", ErrApplyCleanup, err)
	}
	cleanupTransaction = false
	return result, nil
}

func validateDestinationState(ctx context.Context, path string, mode os.FileMode, digest []byte, original os.FileInfo) error {
	info, err := os.Lstat(path)
	if err != nil ||
		!info.Mode().IsRegular() ||
		info.Mode().Perm() != mode ||
		info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 ||
		(original != nil && !os.SameFile(original, info)) {
		return ErrInvalidDestination
	}
	currentDigest, err := digestFile(ctx, path)
	if err != nil || !bytes.Equal(currentDigest, digest) {
		return ErrInvalidDestination
	}
	return nil
}

func restorePrevious(previousPath, destination string) error {
	if err := os.Link(previousPath, destination); err != nil {
		return err
	}
	if err := os.Remove(previousPath); err != nil {
		return err
	}
	return nil
}

func (s *StagedUpdate) remove(path string) error {
	if s.removePath != nil {
		return s.removePath(path)
	}
	return os.Remove(path)
}

// Discard removes a staged update without touching the destination.
func (s *StagedUpdate) Discard() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.used {
		return ErrStagedUpdateUsed
	}
	s.used = true
	return s.remove(s.path)
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(data []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(data)
}
