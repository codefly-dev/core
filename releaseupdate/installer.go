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
	selfupdateapply "github.com/creativeprojects/go-selfupdate/update"
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
func (i *directInstaller) StageAndVerify(ctx context.Context, release Release, destination Destination) (*StagedUpdate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if destination.InstallKind != InstallKindDirect {
		return nil, ErrInstallNotOwned
	}
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
	defer os.Remove(artifactPath)
	if err := i.downloadFile(ctx, selected, i.maxArtifactBytes, artifactFile); err != nil {
		artifactFile.Close()
		return nil, fmt.Errorf("download release artifact: %w", err)
	}
	if err := artifactFile.Close(); err != nil {
		return nil, fmt.Errorf("close artifact staging file: %w", err)
	}
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
		stagedFile.Close()
		if !keepStaged {
			os.Remove(stagedPath)
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

	currentDigest, err := digestFile(ctx, destination.Path)
	if err != nil {
		return nil, fmt.Errorf("hash current executable: %w", err)
	}
	keepStaged = true
	return &StagedUpdate{
		path:              stagedPath,
		destination:       destination.Path,
		mode:              targetInfo.Mode().Perm(),
		stagedDigest:      hash.Sum(nil),
		destinationDigest: currentDigest,
	}, nil
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
	defer response.Body.Close()
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
		defer file.Close()
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
			defer fat.Close()
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
		defer file.Close()
		if file.Cpu != expected {
			return ErrUnsupportedPlatform
		}
		return nil

	default:
		return ErrUnsupportedPlatform
	}
}

func digestFile(ctx context.Context, path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
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
	used              bool
}

// Serializing avoids collisions in go-selfupdate's deterministic sibling names.
var applyMutex sync.Mutex

// Path returns the non-executable staged file path.
func (s *StagedUpdate) Path() string {
	return s.path
}

// Destination returns the explicitly authorized replacement path.
func (s *StagedUpdate) Destination() string {
	return s.destination
}

// Apply atomically replaces the authorized direct binary. Upstream rollback
// restores the prior bytes if the final replacement rename fails.
func (s *StagedUpdate) Apply(ctx context.Context) error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	applyMutex.Lock()
	defer applyMutex.Unlock()
	if s.used {
		return ErrStagedUpdateUsed
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	info, err := os.Lstat(s.destination)
	if err != nil ||
		!info.Mode().IsRegular() ||
		info.Mode().Perm() != s.mode ||
		info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return ErrInvalidDestination
	}
	currentDigest, err := digestFile(ctx, s.destination)
	if err != nil || !bytes.Equal(currentDigest, s.destinationDigest) {
		return ErrInvalidDestination
	}

	staged, err := os.Open(s.path)
	if err != nil {
		return fmt.Errorf("open staged executable: %w", err)
	}
	defer staged.Close()
	newPath := filepath.Join(filepath.Dir(s.destination), "."+filepath.Base(s.destination)+".new")
	if _, err := os.Lstat(newPath); err == nil || !errors.Is(err, os.ErrNotExist) {
		return ErrInvalidDestination
	}

	backup, err := os.CreateTemp(filepath.Dir(s.destination), ".releaseupdate-backup-*")
	if err != nil {
		return fmt.Errorf("reserve rollback path: %w", err)
	}
	backupPath := backup.Name()
	if err := backup.Close(); err != nil {
		os.Remove(backupPath)
		return fmt.Errorf("close rollback path: %w", err)
	}
	if err := os.Remove(backupPath); err != nil {
		return fmt.Errorf("prepare rollback path: %w", err)
	}

	s.used = true
	defer os.Remove(s.path)
	defer os.Remove(newPath)

	err = selfupdateapply.Apply(&contextReader{ctx: ctx, reader: staged}, selfupdateapply.Options{
		TargetPath:  s.destination,
		TargetMode:  s.mode,
		Checksum:    s.stagedDigest,
		OldSavePath: backupPath,
	})
	if err != nil {
		if rollbackErr := selfupdateapply.RollbackError(err); rollbackErr != nil {
			return fmt.Errorf("replace direct binary; restore failed and prior executable remains at %q: %w", backupPath, rollbackErr)
		}
		return fmt.Errorf("replace direct binary: %w", err)
	}
	if err := os.Remove(backupPath); err != nil {
		return fmt.Errorf("replacement succeeded but rollback copy cleanup failed: %w", err)
	}
	return nil
}

// Discard removes a staged update without touching the destination.
func (s *StagedUpdate) Discard() error {
	s.mutex.Lock()
	defer s.mutex.Unlock()
	if s.used {
		return ErrStagedUpdateUsed
	}
	s.used = true
	return os.Remove(s.path)
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
