package manager

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/cheggaaa/pb/v3"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
	"github.com/mholt/archiver"
)

type GithubSource struct {
	Owner string
	Repo  string
}

func toGithubSource(p *resources.Agent) GithubSource {
	return GithubSource{
		Owner: strings.ReplaceAll(p.Publisher, ".", "-"),
		Repo:  fmt.Sprintf("service-%s", p.Name),
	}
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

func Download(ctx context.Context, p *resources.Agent) error {
	w := wool.Get(ctx).In("agents.Download", wool.Field("agent", p.Identifier()))
	releaseURL := DownloadURL(p)
	if !ValidURL(releaseURL) {
		return w.NewError("invalid download URL: %s", releaseURL)
	}
	w.Info(fmt.Sprintf("Downloading agent %s", p.Identifier()))
	w.Debug("downloading", wool.Field("agent", p.Identifier()), wool.Field("url", releaseURL).Debug())

	// #nosec G107
	resp, err := http.Get(releaseURL)
	if err != nil {
		return w.Wrapf(err, "cannot download agent")
	}

	tmp, err := os.CreateTemp("", "service-*.tar.gz")
	if err != nil {
		return w.Wrapf(err, "cannot create temp file")
	}

	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			w.Error("cannot remove temp file", wool.ErrField(err))
		}
	}(tmp.Name())
	// Get the content size from the header
	size := resp.ContentLength

	// Create progress bar
	bar := pb.Full.Start64(size)
	bar.Set(pb.Bytes, true) // Display in bytes instead of default kilobytes

	// Wrap the output file writer with the progress bar to track writes
	writer := bar.NewProxyWriter(tmp)

	// Copy the response body to the file while updating the progress bar
	_, err = io.Copy(writer, resp.Body)
	if err != nil {
		return w.Wrapf(err, "cannot copy agent")
	}
	bar.Finish()

	defer resp.Body.Close()

	// Write the body to file
	_, err = io.Copy(tmp, resp.Body)
	if err != nil {
		return w.Wrapf(err, "cannot copy agent")
	}
	tmpDir, err := os.MkdirTemp("", "service-*")
	if err != nil {
		return w.Wrapf(err, "cannot create temp directory")
	}
	defer os.RemoveAll(tmpDir)
	dest := path.Join(tmpDir, "new")
	err = archiver.Unarchive(tmp.Name(), dest)
	if err != nil {
		return w.Wrapf(err, "cannot unarchive")
	}
	bin, err := p.Path(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot compute agent path")
	}
	binary := path.Join(dest, fmt.Sprintf("service-%s", p.Name))
	exists, err := shared.FileExists(ctx, binary)
	if err != nil {
		return w.Wrapf(err, "cannot check if file exists")
	}
	if exists {
		content, _ := os.ReadDir(dest)
		w.Debug("content ", wool.Field("content", content))
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

	err = MoveFile(binary, target)
	if err != nil {
		return w.Wrapf(err, "cannot move binary")
	}
	err = os.Chmod(bin, 0o755)
	if err != nil {
		return w.Wrapf(err, "cannot chmod binary")
	}
	return nil
}

// CopyFile copies a file from src to dst. If dst does not exist, it is created with permissions copied from src.
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

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	if _, err := io.Copy(destination, source); err != nil {
		return err
	}

	return os.Chmod(dst, sourceFileStat.Mode())
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
