package composition

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/google/go-github/v89/github"
)

type GitHubPackage struct {
	Owner           string
	RepositoryName  string
	RepositoryURL   string
	ArtifactAsset   string
	ProvenanceAsset string
	SignatureAsset  string
}

type GitHubResolver struct {
	Client           *github.Client
	DownloadClient   *http.Client
	Packages         map[string]GitHubPackage
	MaxArtifactBytes int64
	initErr          error
}

func NewGitHubResolver(client *github.Client, downloadClient *http.Client, packages map[string]GitHubPackage) *GitHubResolver {
	var err error
	if client == nil {
		if downloadClient == nil {
			client, err = github.NewClient()
		} else {
			client, err = github.NewClient(github.WithHTTPClient(downloadClient))
		}
	}
	if downloadClient == nil {
		downloadClient = http.DefaultClient
	}
	return &GitHubResolver{Client: client, DownloadClient: downloadClient, Packages: packages, MaxArtifactBytes: 1 << 30, initErr: err}
}

func (resolver *GitHubResolver) Resolve(ctx context.Context, request ResolveRequest) (*Release, error) {
	if resolver.initErr != nil {
		return nil, resolver.initErr
	}
	if _, err := semverStrict(request.Version); err != nil {
		return nil, fmt.Errorf("resolve GitHub module release: %w", err)
	}
	packageSource, exists := resolver.Packages[request.Package]
	if !exists {
		return nil, fmt.Errorf("no GitHub source is registered for module package %q", request.Package)
	}
	return resolver.fetchTag(ctx, packageSource, "v"+request.Version)
}

func (resolver *GitHubResolver) Fetch(ctx context.Context, lock *Lock) (*Release, error) {
	if resolver.initErr != nil {
		return nil, resolver.initErr
	}
	if err := lock.Validate(); err != nil {
		return nil, err
	}
	packageSource, exists := resolver.Packages[lock.Package]
	if !exists {
		return nil, fmt.Errorf("no GitHub source is registered for module package %q", lock.Package)
	}
	if packageSource.RepositoryURL != lock.Source.Repository {
		return nil, fmt.Errorf("locked repository %q does not match registered repository %q", lock.Source.Repository, packageSource.RepositoryURL)
	}
	return resolver.fetchTag(ctx, packageSource, lock.Source.Ref)
}

func (resolver *GitHubResolver) fetchTag(ctx context.Context, packageSource GitHubPackage, tagName string) (*Release, error) {
	if err := packageSource.validate(); err != nil {
		return nil, err
	}
	release, _, err := resolver.Client.Repositories.GetReleaseByTag(ctx, packageSource.Owner, packageSource.RepositoryName, tagName)
	if err != nil {
		return nil, fmt.Errorf("get GitHub module release %s: %w", tagName, err)
	}
	if release.GetDraft() || release.GetTagName() != tagName {
		return nil, fmt.Errorf("GitHub module release %s is draft or has a mismatched tag", tagName)
	}
	commit, err := resolver.peeledCommit(ctx, packageSource, tagName)
	if err != nil {
		return nil, err
	}
	assets := map[string][]byte{}
	for _, name := range []string{packageSource.ArtifactAsset, packageSource.ProvenanceAsset, packageSource.SignatureAsset} {
		asset, err := uniqueReleaseAsset(release.Assets, name)
		if err != nil {
			return nil, err
		}
		limit := int64(1 << 20)
		if name == packageSource.ArtifactAsset {
			limit = resolver.MaxArtifactBytes
			if limit < 1 {
				limit = 1 << 30
			}
		}
		if asset.GetSize() < 0 || int64(asset.GetSize()) > limit {
			return nil, fmt.Errorf("GitHub module release asset %s exceeds its size limit", name)
		}
		assets[name], err = resolver.downloadAsset(ctx, packageSource, asset.GetID(), limit)
		if err != nil {
			return nil, fmt.Errorf("download GitHub module release asset %s: %w", name, err)
		}
	}
	signature := assets[packageSource.SignatureAsset]
	if len(signature) != 64 {
		signature = bytesTrimSpace(signature)
	}
	return &Release{
		Repository: packageSource.RepositoryURL,
		Ref:        tagName,
		Commit:     commit,
		Artifact:   assets[packageSource.ArtifactAsset],
		Provenance: assets[packageSource.ProvenanceAsset],
		Signature:  signature,
	}, nil
}

func (resolver *GitHubResolver) peeledCommit(ctx context.Context, packageSource GitHubPackage, tagName string) (string, error) {
	reference, _, err := resolver.Client.Git.GetRef(ctx, packageSource.Owner, packageSource.RepositoryName, "tags/"+tagName)
	if err != nil {
		return "", fmt.Errorf("resolve GitHub module tag %s: %w", tagName, err)
	}
	object := reference.GetObject()
	for range 16 {
		switch object.GetType() {
		case "commit":
			if !commitPattern.MatchString(object.GetSHA()) {
				return "", errors.New("GitHub module tag resolved to an invalid commit")
			}
			return object.GetSHA(), nil
		case "tag":
			tag, _, err := resolver.Client.Git.GetTag(ctx, packageSource.Owner, packageSource.RepositoryName, object.GetSHA())
			if err != nil {
				return "", fmt.Errorf("peel GitHub module tag %s: %w", tagName, err)
			}
			object = tag.GetObject()
		default:
			return "", fmt.Errorf("GitHub module tag %s targets %q instead of a commit", tagName, object.GetType())
		}
	}
	return "", fmt.Errorf("GitHub module tag %s exceeds the maximum tag depth", tagName)
}

func (resolver *GitHubResolver) downloadAsset(ctx context.Context, packageSource GitHubPackage, id, limit int64) ([]byte, error) {
	reader, _, err := resolver.Client.Repositories.DownloadReleaseAsset(ctx, packageSource.Owner, packageSource.RepositoryName, id, resolver.DownloadClient)
	if err != nil {
		return nil, err
	}
	if reader == nil {
		return nil, errors.New("GitHub returned an asset redirect without content")
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("GitHub module release asset exceeds its download limit")
	}
	return data, nil
}

func uniqueReleaseAsset(assets []*github.ReleaseAsset, name string) (*github.ReleaseAsset, error) {
	var found *github.ReleaseAsset
	for _, asset := range assets {
		if asset.GetName() != name {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("GitHub module release contains duplicate asset %q", name)
		}
		found = asset
	}
	if found == nil {
		return nil, fmt.Errorf("GitHub module release is missing asset %q", name)
	}
	return found, nil
}

func (source GitHubPackage) validate() error {
	if source.Owner == "" || source.RepositoryName == "" || source.RepositoryURL == "" ||
		source.ArtifactAsset == "" || source.ProvenanceAsset == "" || source.SignatureAsset == "" {
		return errors.New("GitHub module package source is incomplete")
	}
	if strings.ContainsAny(source.ArtifactAsset+source.ProvenanceAsset+source.SignatureAsset, "/\\\x00") {
		return errors.New("GitHub module release asset names must be leaf names")
	}
	if source.ArtifactAsset == source.ProvenanceAsset || source.ArtifactAsset == source.SignatureAsset || source.ProvenanceAsset == source.SignatureAsset {
		return errors.New("GitHub module release asset names must be distinct")
	}
	return nil
}

func semverStrict(value string) (string, error) {
	version, err := semver.StrictNewVersion(value)
	if err != nil {
		return "", fmt.Errorf("version %q must be exact semantic version: %w", value, err)
	}
	return version.String(), nil
}

func bytesTrimSpace(value []byte) []byte {
	return []byte(strings.TrimSpace(string(value)))
}
