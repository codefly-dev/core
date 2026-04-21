package manager

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
)

// AgentStore provides a mechanism to pull agent binaries from a remote registry.
// Implementations: OCIStore (OCI-compliant registries), HTTPStore (simple HTTP),
// NixStore (content-addressed via Nix flakes).
type AgentStore interface {
	// Pull downloads an agent binary and returns the local file path.
	// The binary is cached locally — subsequent calls for the same name+version
	// return the cached path without re-downloading.
	Pull(ctx context.Context, agent *resources.Agent) (binaryPath string, err error)

	// Available checks if an agent exists in the store without downloading it.
	Available(ctx context.Context, agent *resources.Agent) (bool, error)
}

// OCIStore pulls agent binaries from an OCI-compliant registry.
// Agent binaries are stored as single-layer OCI artifacts.
//
// Registry layout:
//
//	{registry}/agents/{publisher}/{name}:{version}
//	  └── single layer: service-{name} (the binary, for current OS/arch)
//
// Push example:
//
//	oras push localhost:5111/agents/codefly.dev/go-generic:0.0.1 \
//	  service-go-generic:application/octet-stream
//
// The store uses the OCI distribution spec via plain HTTP calls (no oras CLI needed).
// For simplicity, we use the Docker Registry HTTP API V2.
type OCIStore struct {
	registry string // e.g. "localhost:5111", "ghcr.io/codefly-dev"
	scheme   string // "http" or "https"
	logger   *slog.Logger
}

// NewOCIStore creates a store backed by an OCI registry.
// The registry should be the base URL without scheme (e.g., "localhost:5111").
// Use "http" scheme for local k3d registries, "https" for production.
func NewOCIStore(registry, scheme string, logger *slog.Logger) *OCIStore {
	if scheme == "" {
		if strings.HasPrefix(registry, "localhost") || strings.HasPrefix(registry, "127.0.0.1") {
			scheme = "http"
		} else {
			scheme = "https"
		}
	}
	return &OCIStore{registry: registry, scheme: scheme, logger: logger}
}

// NewOCIStoreFromEnv creates an OCIStore from the AGENT_REGISTRY env var.
// Returns nil if AGENT_REGISTRY is not set.
//
// Format: AGENT_REGISTRY=localhost:5111 (local k3d)
//
//	AGENT_REGISTRY=ghcr.io/codefly-dev (production)
func NewOCIStoreFromEnv(logger *slog.Logger) *OCIStore {
	registry := os.Getenv("AGENT_REGISTRY")
	if registry == "" {
		return nil
	}
	scheme := os.Getenv("AGENT_REGISTRY_SCHEME") // optional, defaults based on registry
	return NewOCIStore(registry, scheme, logger)
}

func (s *OCIStore) repoPath(agent *resources.Agent) string {
	return fmt.Sprintf("agents/%s/%s", agent.Publisher, agent.Name)
}

func (s *OCIStore) tag(agent *resources.Agent) string {
	if agent.Version == "" || agent.Version == "latest" {
		return "latest"
	}
	return agent.Version
}

// Available checks if the agent manifest exists in the registry.
func (s *OCIStore) Available(ctx context.Context, agent *resources.Agent) (bool, error) {
	repo := s.repoPath(agent)
	tag := s.tag(agent)
	url := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", s.scheme, s.registry, repo, tag)

	req, err := http.NewRequestWithContext(ctx, http.MethodHead, url, nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, nil // network error = not available
	}
	defer resp.Body.Close()

	return resp.StatusCode == http.StatusOK, nil
}

// Pull downloads the agent binary from the OCI registry.
// Uses the OCI distribution spec to fetch the single blob layer.
func (s *OCIStore) Pull(ctx context.Context, agent *resources.Agent) (string, error) {
	// Check if already cached locally.
	localPath, err := s.localCachePath(ctx, agent)
	if err != nil {
		return "", fmt.Errorf("compute cache path: %w", err)
	}
	if exists, _ := shared.FileExists(ctx, localPath); exists {
		s.logger.Debug("agent found in local cache", "agent", agent.Identifier(), "path", localPath)
		return localPath, nil
	}

	s.logger.Info("pulling agent from registry", "agent", agent.Identifier(), "registry", s.registry)

	// Step 1: Fetch the manifest to get the blob digest.
	repo := s.repoPath(agent)
	tag := s.tag(agent)
	manifestURL := fmt.Sprintf("%s://%s/v2/%s/manifests/%s", s.scheme, s.registry, repo, tag)

	digest, err := s.fetchBlobDigest(ctx, manifestURL)
	if err != nil {
		return "", fmt.Errorf("fetch manifest for %s:%s: %w", repo, tag, err)
	}

	// Step 2: Download the blob (the agent binary).
	blobURL := fmt.Sprintf("%s://%s/v2/%s/blobs/%s", s.scheme, s.registry, repo, digest)
	if err := s.downloadBlob(ctx, blobURL, localPath); err != nil {
		return "", fmt.Errorf("download blob %s: %w", digest, err)
	}

	// Make executable.
	if err := os.Chmod(localPath, 0o755); err != nil {
		return "", fmt.Errorf("chmod: %w", err)
	}

	s.logger.Info("agent pulled successfully", "agent", agent.Identifier(), "path", localPath)
	return localPath, nil
}

// fetchBlobDigest reads the OCI manifest and returns the digest of the first layer.
func (s *OCIStore) fetchBlobDigest(ctx context.Context, manifestURL string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return "", fmt.Errorf("registry returned %d: %s", resp.StatusCode, string(body))
	}

	// Parse just enough of the manifest to get the first layer digest.
	// OCI manifests have: { layers: [{ digest: "sha256:..." }] }
	// We use simple JSON parsing to avoid importing a full OCI library.
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return extractFirstLayerDigest(body)
}

// downloadBlob fetches a blob from the registry and saves it to dst.
func (s *OCIStore) downloadBlob(ctx context.Context, blobURL, dst string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, blobURL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		return fmt.Errorf("blob download returned %d: %s", resp.StatusCode, string(body))
	}

	// Ensure parent directory exists.
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}

// localCachePath returns where the agent binary should be stored locally.
// Uses the standard codefly agent path so manager.Load() can find it.
func (s *OCIStore) localCachePath(ctx context.Context, agent *resources.Agent) (string, error) {
	return agent.Path(ctx)
}

// extractFirstLayerDigest parses an OCI/Docker manifest and returns the
// digest of the first layer. Avoids importing full OCI libraries.
func extractFirstLayerDigest(manifest []byte) (string, error) {
	// Quick and dirty: find "layers":[{"digest":"sha256:..."
	str := string(manifest)

	// Find layers array
	idx := strings.Index(str, `"layers"`)
	if idx < 0 {
		// Try Docker v2 format: "fsLayers"
		idx = strings.Index(str, `"fsLayers"`)
		if idx < 0 {
			return "", fmt.Errorf("no layers found in manifest")
		}
	}

	// Find first digest after layers
	digestIdx := strings.Index(str[idx:], `"digest"`)
	if digestIdx < 0 {
		return "", fmt.Errorf("no digest found in layers")
	}

	// Extract the digest value
	start := idx + digestIdx
	colonIdx := strings.Index(str[start:], ":")
	if colonIdx < 0 {
		return "", fmt.Errorf("malformed digest field")
	}

	// Find the opening quote of the value
	valueStart := start + colonIdx + 1
	quoteStart := strings.Index(str[valueStart:], `"`)
	if quoteStart < 0 {
		return "", fmt.Errorf("malformed digest value")
	}
	valueStart += quoteStart + 1

	quoteEnd := strings.Index(str[valueStart:], `"`)
	if quoteEnd < 0 {
		return "", fmt.Errorf("unterminated digest value")
	}

	digest := str[valueStart : valueStart+quoteEnd]
	if !strings.HasPrefix(digest, "sha256:") {
		return "", fmt.Errorf("unexpected digest format: %s", digest)
	}

	return digest, nil
}

// PlatformSuffix returns the OS/arch suffix for the current platform.
// Used when pushing platform-specific agent binaries.
func PlatformSuffix() string {
	return fmt.Sprintf("%s_%s", runtime.GOOS, runtime.GOARCH)
}

// NixStore pulls agent binaries by realizing a Nix flake output.
//
// Motivation: OCI/HTTP stores cache by `{publisher}/{name}:{version}` — a
// mutable tag in a registry. Nix outputs are content-addressed, so the
// same flake ref always yields the same store path; cache invalidation is
// automatic and cross-platform selection is handled by Nix itself (no
// PlatformSuffix hand-waving).
//
// Layout convention: the configured flake must expose agents at
// `packages.${system}.agents-{kind}-{name}-{version}`. The build output
// must be either:
//   - a file that is the agent binary, or
//   - a directory containing `bin/service-{name}` (the nixpkgs convention
//     for a package built from a Go module).
//
// Configure via env:
//
//	AGENT_NIX_FLAKE=github:codefly-dev/codefly       # ref
//	AGENT_NIX_FLAKE=github:codefly-dev/codefly/v0.5  # ref pinned to a tag
//	AGENT_NIX_FLAKE=/path/to/local/flake             # local checkout
//
// If unset, NewNixStoreFromEnv returns nil.
type NixStore struct {
	flakeRef string
	logger   *slog.Logger
}

// NewNixStore creates a NixStore for an explicit flake ref.
func NewNixStore(flakeRef string, logger *slog.Logger) *NixStore {
	return &NixStore{flakeRef: flakeRef, logger: logger}
}

// NewNixStoreFromEnv creates a NixStore from AGENT_NIX_FLAKE. Returns nil
// if unset or if `nix` is not on PATH.
func NewNixStoreFromEnv(logger *slog.Logger) *NixStore {
	ref := os.Getenv("AGENT_NIX_FLAKE")
	if ref == "" {
		return nil
	}
	if _, err := exec.LookPath("nix"); err != nil {
		return nil
	}
	return &NixStore{flakeRef: ref, logger: logger}
}

func (s *NixStore) attrFor(agent *resources.Agent) string {
	// Nix attribute names can't contain `:` or `/`. Kind is e.g.
	// "codefly:service" — drop the prefix, use just "service".
	kind := string(agent.Kind)
	if i := strings.LastIndex(kind, ":"); i >= 0 {
		kind = kind[i+1:]
	}
	return fmt.Sprintf("agents-%s-%s-%s", kind, agent.Name, agent.Version)
}

func (s *NixStore) fullRef(agent *resources.Agent) string {
	return fmt.Sprintf("%s#%s", s.flakeRef, s.attrFor(agent))
}

// Available asks nix to evaluate the attribute without building it.
// Cheap compared to Pull: just flake eval, no derivation realization.
func (s *NixStore) Available(ctx context.Context, agent *resources.Agent) (bool, error) {
	ref := s.fullRef(agent)
	// #nosec G204 -- flakeRef/attr are built from env + validated Agent fields.
	cmd := exec.CommandContext(ctx,
		"nix", "--extra-experimental-features", "nix-command flakes",
		"eval", "--raw", ref+".drvPath")
	if err := cmd.Run(); err != nil {
		return false, nil
	}
	return true, nil
}

// Pull realizes the flake output and returns the path of the agent binary.
// Nix handles its own content-addressed cache under /nix/store, so repeat
// Pulls for the same ref are no-ops once the derivation is realized.
func (s *NixStore) Pull(ctx context.Context, agent *resources.Agent) (string, error) {
	ref := s.fullRef(agent)
	if s.logger != nil {
		s.logger.Info("realizing agent via nix", "agent", agent.Identifier(), "ref", ref)
	}

	// #nosec G204
	cmd := exec.CommandContext(ctx,
		"nix", "--extra-experimental-features", "nix-command flakes",
		"build", "--no-link", "--print-out-paths", ref)
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return "", fmt.Errorf("nix build %s failed: %s",
				ref, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("nix build %s: %w", ref, err)
	}
	outPath := strings.TrimSpace(string(out))
	if outPath == "" {
		return "", fmt.Errorf("nix build %s produced no output path", ref)
	}

	// Resolve to the actual binary.
	return resolveNixAgentBinary(outPath, agent)
}

// resolveNixAgentBinary inspects a /nix/store output path and returns the
// path to the executable agent binary. Two conventions are accepted:
//   - outPath is itself a file (a single static binary).
//   - outPath is a directory containing bin/service-{name} (nixpkgs Go
//     package convention).
func resolveNixAgentBinary(outPath string, agent *resources.Agent) (string, error) {
	info, err := os.Stat(outPath)
	if err != nil {
		return "", fmt.Errorf("stat nix output %s: %w", outPath, err)
	}
	if !info.IsDir() {
		return outPath, nil
	}
	candidate := filepath.Join(outPath, "bin", "service-"+agent.Name)
	if _, err := os.Stat(candidate); err == nil {
		return candidate, nil
	}
	// Fall back to the first executable under bin/.
	entries, err := os.ReadDir(filepath.Join(outPath, "bin"))
	if err != nil {
		return "", fmt.Errorf("nix output %s has no bin/: %w", outPath, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			return filepath.Join(outPath, "bin", e.Name()), nil
		}
	}
	return "", fmt.Errorf("nix output %s: no binary found under bin/", outPath)
}
