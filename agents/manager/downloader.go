package manager

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/cheggaaa/pb/v3"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

type GithubSource struct {
	Owner string
	Repo  string
}

func toGithubSource(p *resources.Agent) (GithubSource, error) {
	if _, err := p.Proto(); err != nil {
		return GithubSource{}, err
	}
	registration, err := resources.AgentKindRegistrationFor(p.Kind)
	if err != nil {
		return GithubSource{}, err
	}
	return GithubSource{
		Owner: strings.ReplaceAll(p.Publisher, ".", "-"),
		Repo:  registration.GitHubRepository(p.Name),
	}, nil
}

func ValidURL(s string) bool {
	u, err := url.Parse(s)
	if err != nil {
		return false
	}
	if u.Host != "github.com" {
		return false
	}
	return true
}

func Downloaded(ctx context.Context, p *resources.Agent) (bool, error) {
	w := wool.Get(ctx).In("agents.Downloaded", wool.Field("agent", p.Identifier()))
	bin, err := p.Path(ctx)
	if err != nil {
		return false, w.Wrapf(err, "cannot compute agent path")
	}
	w.Debug("checking if agent is downloaded", wool.Field("path", bin))
	exists, err := shared.FileExists(ctx, bin)
	if err != nil {
		return false, w.Wrapf(err, "cannot check if file exists")
	}
	return exists, nil
}

// agentDownloadClient bounds the ways a release download can stall before the
// asset starts flowing — an unreachable host, a TLS handshake that never
// completes, a proxy that swallows the response. It deliberately sets no
// client-wide Timeout: once headers are in, a large asset over a slow link is
// legitimate, and the caller's context is what ends it early.
var agentDownloadClient = &http.Client{
	Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
		// Parity with http.DefaultTransport, which the previous http.Get got
		// for free: without these an idle keep-alive connection to the release
		// CDN is pinned for the life of the process.
		MaxIdleConns:    100,
		IdleConnTimeout: 90 * time.Second,
	},
}

// writeArchive streams body into f, digesting it on the way, and closes it.
// extractTarGz reopens the archive by path, so the write handle must not
// outlive the copy — every return here closes it, including the ones that fail.
func writeArchive(f *os.File, body io.Reader, size int64) (digest string, err error) {
	defer func() { _ = f.Close() }()

	bar := pb.Full.Start64(size)
	bar.Set(pb.Bytes, true) // Display in bytes instead of default kilobytes

	hash := sha256.New()
	if _, err := io.Copy(io.MultiWriter(bar.NewProxyWriter(f), hash), body); err != nil {
		return "", err
	}
	bar.Finish()

	// Closed here rather than only by the defer: a write error that surfaces
	// at close must reach the caller instead of appearing after the archive
	// has already been extracted.
	if err := f.Close(); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// releaseDigestFile is the sidecar Download writes beside an installed agent:
// the SHA-256 of the executable as it was installed from its release, in
// sha256sum's format. Its name begins with a dot so no scan of the cache
// directory — local latest, installed inventory, the CLI's version listing —
// reads it as an agent. Its presence says the executable came from a release
// through this downloader; its value is what Load holds the executable to.
func releaseDigestFile(executable string) string {
	return filepath.Join(filepath.Dir(executable), "."+filepath.Base(executable)+".sha256")
}

// InstalledReleaseDigest reads the digest an agent was installed from its
// release with: the hex SHA-256 of its executable at install time. It is ""
// with no error when the agent is cached but was never installed from a release
// by this downloader — a local build, or an install older than the sidecar.
func InstalledReleaseDigest(ctx context.Context, p *resources.Agent) (string, error) {
	bin, err := p.Path(ctx)
	if err != nil {
		return "", err
	}
	return readReleaseDigest(bin)
}

func readReleaseDigest(executable string) (string, error) {
	raw, err := os.ReadFile(releaseDigestFile(executable))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	entries, err := parseChecksums(raw)
	if err != nil {
		return "", fmt.Errorf("%s: %w", releaseDigestFile(executable), err)
	}
	digest, ok := entries[filepath.Base(executable)]
	if !ok {
		return "", fmt.Errorf("%s records no digest for %s", releaseDigestFile(executable), filepath.Base(executable))
	}
	return digest, nil
}

// writeReleaseDigest replaces the record atomically: a concurrent reader sees
// the previous record or this one, never an empty or partial file. It is
// world-readable like the executable it describes (a digest is not a secret),
// so a cache another user installed into is still readable.
func writeReleaseDigest(executable, digest string) error {
	staged, err := stageReleaseDigest(executable, digest)
	if err != nil {
		return err
	}
	defer os.Remove(staged)
	return os.Rename(staged, releaseDigestFile(executable))
}

// stageReleaseDigest writes the record to a synced temporary file beside it,
// ready to be renamed into place.
func stageReleaseDigest(executable, digest string) (string, error) {
	line := fmt.Sprintf("%s  %s\n", digest, filepath.Base(executable))
	tmp, err := os.CreateTemp(filepath.Dir(executable), ".agent-digest-*")
	if err != nil {
		return "", err
	}
	staged := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(staged)
		}
	}()
	if _, err := tmp.WriteString(line); err != nil {
		return "", err
	}
	if err := tmp.Chmod(0o644); err != nil {
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	ok = true
	return staged, nil
}

// installRecordedExecutable puts staged — a complete, synced file in target's
// directory — at target, with its digest recorded beside it.
//
// Several loads of one agent install it concurrently (every service sharing an
// agent, on a cold cache), so readers must not see the release's own bytes
// without its record. When the record already names these bytes — the common
// case, the same release installed twice — the executable is swapped under a
// record that stays true throughout. Otherwise both files are staged first and
// the stale record goes before the executable moves, so the window without a
// record is two renames wide. Two concurrent installs of the same name with
// DIFFERENT bytes can still interleave and leave one's executable beside the
// other's record; that fails safe — admission refuses the mismatch, naming both
// digests, and deleting the entry recovers — rather than admitting either.
func installRecordedExecutable(staged, target, digest string) error {
	if recorded, err := readReleaseDigest(target); err == nil && recorded == digest {
		return os.Rename(staged, target)
	}
	record, err := stageReleaseDigest(target, digest)
	if err != nil {
		return err
	}
	defer os.Remove(record)
	if err := os.Remove(releaseDigestFile(target)); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(staged, target); err != nil {
		return err
	}
	return os.Rename(record, releaseDigestFile(target))
}

func Download(ctx context.Context, p *resources.Agent) error {
	w := wool.Get(ctx).In("agents.Download", wool.Field("agent", p.Identifier()))
	registration, err := resources.AgentKindRegistrationFor(p.Kind)
	if err != nil {
		return w.Wrap(err)
	}
	if !registration.AutoDownload {
		return w.NewError("GitHub auto-download is disabled for agent kind %s", p.Kind)
	}
	releaseURL, err := DownloadURL(p)
	if err != nil {
		return w.Wrap(err)
	}
	if !ValidURL(releaseURL) {
		return w.NewError("invalid download URL: %s", releaseURL)
	}
	w.Info(fmt.Sprintf("Downloading agent %s", p.Identifier()))
	w.Debug("downloading", wool.Field("agent", p.Identifier()), wool.Field("url", releaseURL).Debug())

	asset, err := openAgentRelease(ctx, releaseURL)
	if err != nil {
		return w.Wrapf(err, "cannot download agent")
	}
	defer asset.Body.Close()

	tmp, err := os.CreateTemp("", "agent-*.tar.gz")
	if err != nil {
		return w.Wrapf(err, "cannot create temp file")
	}

	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			w.Error("cannot remove temp file", wool.ErrField(err))
		}
	}(tmp.Name())
	archiveDigest, err := writeArchive(tmp, asset.Body, asset.Size)
	if err != nil {
		return w.Wrapf(err, "cannot copy agent")
	}
	warning, err := checkArchiveDigest(p.Identifier(), asset, archiveDigest)
	if err != nil {
		return w.Wrap(err)
	}
	if warning != "" {
		w.Warn(warning, wool.Field("url", releaseURL))
	}

	tmpDir, err := os.MkdirTemp("", "agent-*")
	if err != nil {
		return w.Wrapf(err, "cannot create temp directory")
	}
	defer os.RemoveAll(tmpDir)
	dest := path.Join(tmpDir, "new")
	if err := extractTarGz(tmp.Name(), dest); err != nil {
		return w.Wrapf(err, "cannot unarchive")
	}
	binary := path.Join(dest, registration.ExecutableName(p.Name))
	if err := os.Chmod(binary, 0o755); err != nil {
		return w.Wrapf(err, "cannot chmod binary")
	}
	target, err := p.Path(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot compute agent path")
	}
	// create folder if needed
	folder := filepath.Dir(target)
	_, err = shared.CheckDirectoryOrCreate(ctx, folder)
	if err != nil {
		return w.Wrapf(err, "cannot create agent folder")
	}

	if err := ctx.Err(); err != nil {
		return err
	}
	binaryDigest, err := fileSHA256(binary)
	if err != nil {
		return w.Wrapf(err, "cannot digest the extracted agent")
	}
	staged, err := stageCopy(binary, folder)
	if err != nil {
		return w.Wrapf(err, "cannot stage binary")
	}
	defer os.Remove(staged)
	if err := installRecordedExecutable(staged, target, binaryDigest); err != nil {
		return w.Wrapf(err, "cannot install binary with its digest")
	}
	return nil
}

// checkArchiveDigest holds downloaded bytes to the digest the release records
// for them, before anything is extracted. Without a digest nothing can be held,
// and that is said rather than passed over: the archive is then trusted on its
// name alone, which is the state every install was in before this check.
func checkArchiveDigest(identifier string, asset *ReleaseAsset, archiveDigest string) (warning string, err error) {
	switch {
	case asset.Digest == "":
		return "release publishes no digest for this archive; installing it unverified", nil
	case archiveDigest != asset.Digest:
		return "", fmt.Errorf("refusing to install %s: the downloaded archive is sha256:%s but the release's %s record says sha256:%s", identifier, archiveDigest, asset.DigestSource, asset.Digest)
	}
	return "", nil
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path) //nolint:gosec // G304: path is the executable this downloader just extracted
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// extractTarGz decompresses gzip then extracts the tar archive at src into
// dst. Replaces mholt/archiver.Unarchive (which had two unpatched path
// traversal CVEs). Only .tar.gz is supported — this is all GitHub release
// assets use. Entries whose resolved path escapes dst are rejected
// (defense against zip-slip / CVE-2025-3605).
func extractTarGz(src, dst string) error {
	f, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open archive: %w", err)
	}
	defer f.Close()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	if err := os.MkdirAll(dst, 0o755); err != nil {
		return fmt.Errorf("mkdir dst: %w", err)
	}
	absDst, err := filepath.Abs(dst)
	if err != nil {
		return fmt.Errorf("abs dst: %w", err)
	}

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}

		// Zip-slip defense: reject any entry whose path after Join escapes dst.
		target := filepath.Join(absDst, hdr.Name)
		rel, err := filepath.Rel(absDst, target)
		if err != nil || strings.HasPrefix(rel, "..") || rel == ".." {
			return fmt.Errorf("illegal path in archive: %s", hdr.Name)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, os.FileMode(hdr.Mode)&0o777); err != nil {
				return fmt.Errorf("mkdir %s: %w", target, err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return fmt.Errorf("mkdir parent of %s: %w", target, err)
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return fmt.Errorf("create %s: %w", target, err)
			}
			// Limit copy size to avoid decompression bombs — GitHub release
			// agent binaries are well under 1 GiB in practice.
			if _, err := io.CopyN(out, tr, 1<<30); err != nil && err != io.EOF {
				out.Close()
				return fmt.Errorf("write %s: %w", target, err)
			}
			if err := out.Close(); err != nil {
				return fmt.Errorf("close %s: %w", target, err)
			}
		case tar.TypeSymlink, tar.TypeLink:
			// Agent archives are plain binaries — no need for symlinks. Skip
			// to avoid another class of traversal attacks.
			continue
		default:
			// Skip unknown types (pax headers, globals, etc.) — harmless.
		}
	}
}

// CopyFile publishes a complete file with its source permissions by atomic rename.
func CopyFile(src, dst string) error {
	sourceFileStat, err := os.Stat(src)
	if err != nil {
		return err
	}
	if !sourceFileStat.Mode().IsRegular() {
		return os.ErrInvalid
	}
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	staged, err := stageFrom(source, sourceFileStat.Mode().Perm(), filepath.Dir(dst))
	if err != nil {
		return err
	}
	defer os.Remove(staged)
	return os.Rename(staged, dst)
}

// stageCopy copies src into a synced temporary file in dir, ready to be
// renamed into place; the caller removes it if the rename never happens.
func stageCopy(src, dir string) (string, error) {
	info, err := os.Stat(src)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() {
		return "", os.ErrInvalid
	}
	source, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer source.Close()
	return stageFrom(source, info.Mode().Perm(), dir)
}

func stageFrom(source io.Reader, perm os.FileMode, dir string) (string, error) {
	destination, err := os.CreateTemp(dir, ".agent-install-*")
	if err != nil {
		return "", err
	}
	staged := destination.Name()
	ok := false
	defer func() {
		_ = destination.Close()
		if !ok {
			_ = os.Remove(staged)
		}
	}()
	if _, err := io.Copy(destination, source); err != nil {
		return "", err
	}
	if err := destination.Chmod(perm); err != nil {
		return "", err
	}
	if err := destination.Sync(); err != nil {
		return "", err
	}
	if err := destination.Close(); err != nil {
		return "", err
	}
	ok = true
	return staged, nil
}

// MoveFile attempts to rename the file, and if it fails due to an invalid cross-device link,
// it falls back to copying the file and then removing the original file.
func MoveFile(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		if err := CopyFile(src, dst); err != nil {
			return err
		}
		return os.Remove(src)
	}
	return nil
}
