package manager

import (
	"archive/tar"
	"compress/gzip"
	"context"
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

// fetchRelease issues the asset GET under the caller's context, so cancelling
// it aborts the transfer at any point — including mid-body, which no timeout
// on the request can cover.
//
// It does not check the host: callers must pass a URL they have already put
// through ValidURL, as Download does. The check lives there rather than here
// because the tests that cover the stall and cancellation paths have to reach
// a local server.
func fetchRelease(ctx context.Context, releaseURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releaseURL, nil)
	if err != nil {
		return nil, err
	}
	return agentDownloadClient.Do(req)
}

// writeArchive streams body into f and closes it. extractTarGz reopens the
// archive by path, so the write handle must not outlive the copy — every
// return here closes it, including the ones that fail.
func writeArchive(f *os.File, body io.Reader, size int64) error {
	defer func() { _ = f.Close() }()

	bar := pb.Full.Start64(size)
	bar.Set(pb.Bytes, true) // Display in bytes instead of default kilobytes

	if _, err := io.Copy(bar.NewProxyWriter(f), body); err != nil {
		return err
	}
	bar.Finish()

	// Closed here rather than only by the defer: a write error that surfaces
	// at close must reach the caller instead of appearing after the archive
	// has already been extracted.
	return f.Close()
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

	resp, err := fetchRelease(ctx, releaseURL)
	if err != nil {
		return w.Wrapf(err, "cannot download agent")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return w.NewError("unexpected status code %d when downloading agent", resp.StatusCode)
	}

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
	if err = writeArchive(tmp, resp.Body, resp.ContentLength); err != nil {
		return w.Wrapf(err, "cannot copy agent")
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
	err = CopyFile(binary, target)
	if err != nil {
		return w.Wrapf(err, "cannot install binary")
	}
	return nil
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

	destination, err := os.CreateTemp(filepath.Dir(dst), ".agent-install-*")
	if err != nil {
		return err
	}
	defer destination.Close()
	defer os.Remove(destination.Name())

	if _, err := io.Copy(destination, source); err != nil {
		return err
	}

	if err := destination.Chmod(sourceFileStat.Mode().Perm()); err != nil {
		return err
	}
	if err := destination.Sync(); err != nil {
		return err
	}
	if err := destination.Close(); err != nil {
		return err
	}
	return os.Rename(destination.Name(), dst)
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
