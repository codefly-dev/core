package agents

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/google/go-github/v37/github"

	"github.com/cheggaaa/pb/v3"
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/golor"
	"github.com/hashicorp/go-plugin"
	"github.com/mholt/archiver"
)

type AgentContext interface {
	Key(p *configurations.Agent, unique string) string
	Default() plugin.Plugin
}

type Pluggable interface {
	ImplementationKind() string
	Path() (string, error)
	Name() string
	Unique() string
}

var inUse map[string]*plugin.Client

func init() {
	inUse = make(map[string]*plugin.Client)
}

func Cleanup(unique string) {
	logger := shared.NewLogger("agents.Cleanup<%s>", unique)
	if client, ok := inUse[unique]; ok {
		client.Kill()
		return
	}
	logger.Oops("cannot find agent client for <%s> in use", unique)
}

// Name is what the agent will be identified as: for clean up

func Load[P AgentContext, Instance any](p *configurations.Agent, unique string, opts ...Option) (*Instance, error) {
	logger := shared.NewLogger("agents.Load<%s>", p.Unique())
	opt := Options{}
	for _, o := range opts {
		o(&opt)
	}
	if p == nil {
		return nil, logger.Errorf("agent cannot be nil")
	}
	// if version is latest, fetch latest release number
	if p.Version == "latest" {
		err := PinToLatestRelease(p)
		if err != nil {
			return nil, logger.Wrapf(err, "cannot get latest release")
		}
	}
	var this P
	bin, err := p.Path()
	if err != nil {
		return nil, logger.Wrapf(err, "cannot compute agent path")
	}
	logger.Tracef("looking agent %v in %s", p, bin)
	// Already loaded or download
	if _, err := exec.LookPath(bin); err != nil {
		err := Download(p)
		if err != nil {
			return nil, logger.Errorf("cannot find agent <%s>: %v", p.Name(), err)
		}
	}
	logger.Tracef("loading agent from local path <%s>", bin)

	placeholder := this.Default()
	pluginMap := map[string]plugin.Plugin{this.Key(p, unique): placeholder}

	clientLogger := NewServerLogger() // shared.Debug() || shared.Trace())

	if opt.Quiet {
		clientLogger = NoLogger()
	}
	client := plugin.NewClient(&plugin.ClientConfig{
		HandshakeConfig:  HandshakeConfig,
		Plugins:          pluginMap,
		Cmd:              exec.Command(bin),
		AllowedProtocols: []plugin.Protocol{plugin.ProtocolGRPC},
		Logger:           clientLogger,
	})
	logger.Tracef("agent client created for <%s>", unique)
	inUse[unique] = client
	// Connect via RPC
	grpcClient, err := client.Client()
	if err != nil {
		return nil, logger.Errorf("cannot create gRPC client: %v", err)
	}
	// Request the platform
	raw, err := grpcClient.Dispense(this.Key(p, unique))
	if err != nil {
		return nil, logger.Errorf("cannot dispense agent <%s> from gRPC client: %v", p.Name(), err)
	}
	u := raw.(*Instance)
	if u == nil {
		return nil, logger.Errorf("agent [%s] does not implement the proper interface", p.Unique())
	}
	return u, nil
}

type GithubSource struct {
	Owner string
	Repo  string
}

func toGithubSource(p *configurations.Agent) GithubSource {
	return GithubSource{
		Owner: strings.Replace(p.Publisher, ".", "-", -1),
		Repo:  fmt.Sprintf("service-%s", p.Identifier),
	}
}

func PinToLatestRelease(p *configurations.Agent) error {
	logger := shared.NewLogger("agents.PinToLatestRelease<%s>", p.Unique())
	client := github.NewClient(nil)
	source := toGithubSource(p)
	release, _, err := client.Repositories.GetLatestRelease(context.Background(), source.Owner, source.Repo)
	if err != nil {
		return logger.Wrapf(err, "cannot get latest release")
	}
	tag := release.GetTagName()
	p.Version = strings.Replace(tag, "v", "", -1)
	return nil
}

func Download(p *configurations.Agent) error {
	logger := shared.NewLogger("agents.Download<%s>", p.Unique())
	golor.Println(`#(blue,bold)[Downloading agent {{.Publisher}}::{{.Identifier}} Version {{.Version}}]`, p)

	releaseURL := DownloadURL(p)

	logger.TODO("Publisher to URL: %v", releaseURL)
	resp, err := http.Get(releaseURL)
	if err != nil {
		return logger.Wrapf(err, "cannot download agent")
	}

	tmp, err := os.CreateTemp("", "service-*.tar.gz")
	if err != nil {
		return logger.Wrapf(err, "cannot create temp file")
	}

	defer func(name string) {
		err := os.Remove(name)
		if err != nil {
			logger.Info("cannot remove temp file <%s>: %v", name, err)
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
		return logger.Wrapf(err, "cannot copy agent")
	}
	bar.Finish()

	defer resp.Body.Close()

	// Write the body to file
	_, err = io.Copy(tmp, resp.Body)
	if err != nil {
		return logger.Wrapf(err, "cannot copy agent")
	}
	tmpDir, err := os.MkdirTemp("", "service-*")
	if err != nil {
		return logger.Wrapf(err, "cannot create temp directory")
	}
	defer os.RemoveAll(tmpDir)
	dest := path.Join(tmpDir, "new")
	err = archiver.Unarchive(tmp.Name(), dest)
	if err != nil {
		return logger.Wrapf(err, "cannot unarchive")
	}
	bin, err := p.Path()

	binary := path.Join(dest, fmt.Sprintf("service-%s", p.Identifier))
	if !shared.FileExists(binary) {
		content, _ := os.ReadDir(dest)
		fmt.Println("content ", content)
	}
	if err != nil {
		return logger.Wrapf(err, "cannot compute agent path")
	}
	target, err := p.Path()
	if err != nil {
		return logger.Wrapf(err, "cannot compute agent path")
	}
	// create folder if needed
	folder := filepath.Dir(target)
	err = shared.CheckDirectoryOrCreate(folder)
	if err != nil {
		return logger.Wrapf(err, "cannot create agent folder")
	}

	cmd := exec.Command("mv", binary, target)
	if err = cmd.Run(); err != nil {
		return logger.Wrapf(err, "cannot move binary")
	}
	err = os.Chmod(bin, 0o755)
	if err != nil {
		return logger.Wrapf(err, "cannot chmod binary")
	}
	return nil
}
