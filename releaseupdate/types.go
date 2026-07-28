package releaseupdate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"time"

	"github.com/Masterminds/semver/v3"
)

var (
	ErrReleaseNotFound     = errors.New("release not found")
	ErrAssetNotFound       = errors.New("release asset not found")
	ErrCacheMiss           = errors.New("conditional response has no cached releases")
	ErrRateLimited         = errors.New("release source rate limited")
	ErrInvalidRelease      = errors.New("invalid release metadata")
	ErrUnsupportedChannel  = errors.New("unsupported release channel")
	ErrUnsupportedPlatform = errors.New("unsupported release platform")
	ErrInstallNotOwned     = errors.New("installation is not directly owned")
	ErrInvalidDestination  = errors.New("invalid update destination")
	ErrDownloadLimit       = errors.New("download exceeds configured limit")
	ErrUnsafeArchive       = errors.New("archive contains an unsafe path")
	ErrAuthenticity        = errors.New("publisher authenticity verification failed")
	ErrChecksum            = errors.New("artifact checksum verification failed")
	ErrUpdateNotAvailable  = errors.New("release is not an authorized update")
	ErrApplyCleanup        = errors.New("replacement committed with cleanup remaining")
	ErrStagedUpdateUsed    = errors.New("staged update has already been used")
)

// Version is a validated semantic version.
type Version struct {
	value string
}

// ParseVersion parses and normalizes a semantic version.
func ParseVersion(value string) (Version, error) {
	parsed, err := semver.StrictNewVersion(strings.TrimPrefix(value, "v"))
	if err != nil {
		return Version{}, fmt.Errorf("parse version %q: %w", value, err)
	}
	return Version{value: parsed.String()}, nil
}

// String returns the normalized semantic version without a leading "v".
func (v Version) String() string {
	return v.value
}

// Compare returns -1, 0, or 1 when v is less than, equal to, or greater than other.
func (v Version) Compare(other Version) int {
	left := semver.MustParse(v.value)
	right := semver.MustParse(other.value)
	return left.Compare(right)
}

func (v Version) prerelease() bool {
	return semver.MustParse(v.value).Prerelease() != ""
}

// MarshalText serializes a Version as normalized semantic-version text.
func (v Version) MarshalText() ([]byte, error) {
	if v.value == "" {
		return nil, errors.New("cannot marshal an empty version")
	}
	return []byte(v.value), nil
}

// UnmarshalText validates semantic-version text.
func (v *Version) UnmarshalText(text []byte) error {
	parsed, err := ParseVersion(string(text))
	if err != nil {
		return err
	}
	*v = parsed
	return nil
}

// MarshalJSON serializes a Version as a JSON string.
func (v Version) MarshalJSON() ([]byte, error) {
	text, err := v.MarshalText()
	if err != nil {
		return nil, err
	}
	return json.Marshal(string(text))
}

// UnmarshalJSON validates a JSON string as a semantic version.
func (v *Version) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	return v.UnmarshalText([]byte(text))
}

// Channel controls whether prereleases are eligible.
type Channel string

const (
	ChannelStable Channel = "stable"
	ChannelBeta   Channel = "beta"
)

// DowngradePolicy controls whether a lower latest release is actionable.
type DowngradePolicy string

const (
	DowngradeDisallow DowngradePolicy = "disallow"
	DowngradeAllow    DowngradePolicy = "allow"
)

// InstallKind reports which system owns an installation.
type InstallKind string

const (
	InstallKindUnknown           InstallKind = "unknown"
	InstallKindDirect            InstallKind = "direct"
	InstallKindHomebrew          InstallKind = "homebrew"
	InstallKindTauri             InstallKind = "tauri"
	InstallKindApplicationBundle InstallKind = "application-bundle"
	InstallKindManaged           InstallKind = "managed"
)

// Repository identifies a provider repository.
type Repository struct {
	Owner string
	Name  string
}

// Platform identifies a Go operating system and architecture pair.
type Platform struct {
	OS   string
	Arch string
}

// CurrentPlatform returns the platform of the running binary.
func CurrentPlatform() Platform {
	return Platform{OS: runtime.GOOS, Arch: runtime.GOARCH}
}

// Asset describes an immutable release artifact.
type Asset struct {
	ID          int64
	Name        string
	DownloadURL string
	Size        int64
	Platform    Platform
}

// Release is provider-neutral release metadata. Asset is the platform-selected
// artifact; Assets contains the complete published asset set needed for
// verification.
type Release struct {
	ID          int64
	Version     Version
	Name        string
	URL         string
	PublishedAt time.Time
	Prerelease  bool
	Draft       bool
	Asset       Asset
	Assets      []Asset
}

// Request describes one read-only discovery operation.
type Request struct {
	Repository   Repository
	Current      Version
	Channel      Channel
	Platform     Platform
	ArtifactName string
	InstallKind  InstallKind
	Downgrade    DowngradePolicy
}

// Status is a caller-renderable discovery result.
type Status struct {
	Current     Version
	Latest      Version
	Available   bool
	Channel     Channel
	Downgrade   DowngradePolicy
	Release     Release
	InstallKind InstallKind
	CheckedAt   time.Time
	FromCache   bool
	Stale       bool
}

// CacheMetadata contains HTTP validators for a release collection.
type CacheMetadata struct {
	ETag         string
	LastModified string
	ValidatedAt  time.Time
}

// CacheEntry is the complete caller-persisted discovery state.
type CacheEntry struct {
	Metadata CacheMetadata
	Releases []Release
}

// Store persists release metadata without prescribing filesystem state.
type Store interface {
	Load(context.Context, string) (CacheEntry, bool, error)
	Save(context.Context, string, CacheEntry) error
}

// Checker discovers releases without mutating an installation.
type Checker interface {
	Check(context.Context, Request) (Status, error)
}

// Installer prepares an authorized, verified direct-binary update from a
// discovery Status.
type Installer interface {
	StageAndVerify(context.Context, Status, Destination) (*StagedUpdate, error)
}

// Destination grants mutation authority for one explicitly named installation.
type Destination struct {
	Path             string
	StagingDirectory string
	InstallKind      InstallKind
}

// RateLimitError reports when cached data was returned because GitHub refused
// a fresh request.
type RateLimitError struct {
	RetryAt time.Time
}

// ApplyResult reports whether an apply operation committed the replacement and
// where the prior executable remains when cleanup or rollback needs attention.
type ApplyResult struct {
	Applied      bool
	PreviousPath string
	CleanupPath  string
}

func (e *RateLimitError) Error() string {
	if e.RetryAt.IsZero() {
		return ErrRateLimited.Error()
	}
	return fmt.Sprintf("%s until %s", ErrRateLimited, e.RetryAt.UTC().Format(time.RFC3339))
}

func (e *RateLimitError) Unwrap() error {
	return ErrRateLimited
}
