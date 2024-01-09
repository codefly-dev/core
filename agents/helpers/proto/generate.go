package proto

import (
	"context"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/codefly-dev/core/builders"
	"github.com/codefly-dev/core/runners"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"
)

type Proto struct {
	Dir     string
	version string

	// Keep the proto hash for cashing
	hash       string
	dependency *builders.Dependency
}

func NewProto(ctx context.Context, dir string) (*Proto, error) {
	w := wool.Get(ctx).In("proto.NewProto")
	version, err := version(ctx)
	if err != nil {
		return nil, w.Wrapf(err, "cannot get version")
	}
	return &Proto{
		Dir:        dir,
		version:    version,
		dependency: &builders.Dependency{Components: []string{dir}, Ignore: shared.NewSelect("*.proto")},
		hash:       LoadHash(hashfile(dir)),
	}, nil
}

func hashfile(dir string) string {
	return filepath.Join(dir, ".proto.hash")
}

func LoadHash(hashFile string) string {
	f, err := os.Open(hashFile)
	if err != nil {
		return ""
	}
	defer f.Close()
	var hash string
	_, err = fmt.Fscanf(f, "%s", &hash)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(hash)
}

func version(ctx context.Context) (string, error) {
	w := wool.Get(ctx).In("proto.version")
	conf, err := configurations.LoadFromFs[configurations.Info](shared.Embed(info))
	if err != nil {
		return "", w.Wrapf(err, "cannot load info for companion")
	}
	// check we have a valid semantic version
	v, err := semver.NewVersion(conf.Version)
	if err != nil {
		return "", w.Wrapf(err, "cannot parse version <%s>", conf.Version)
	}
	return v.String(), nil
}

//go:embed info.codefly.yaml
var info embed.FS

func (g *Proto) Generate(ctx context.Context) error {
	w := wool.Get(ctx).In("proto.Generate")
	hash, err := g.dependency.Hash()
	if err != nil {
		return w.Wrapf(err, "cannot hash proto files")
	}
	w.Debug("comparing hashes", wool.Field("stored", g.hash), wool.Field("current", hash))
	if hash == g.hash {
		return nil
	}
	image := fmt.Sprintf("codeflydev/companion:%s", g.version)
	volume := fmt.Sprintf("%s:/workspace", g.Dir)
	runner := runners.Runner{Dir: g.Dir, Bin: "docker", Args: []string{"run", "--rm", "-v", volume, image, "buf", "mod", "update"}}
	err = runner.Run(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot generate code from buf")
	}
	runner = runners.Runner{Dir: g.Dir, Bin: "docker", Args: []string{"run", "--rm", "-v", volume, image, "buf", "generate"}}
	w.Debug("Generating code from buf...")
	err = runner.Run(ctx)
	if err != nil {
		return w.Wrapf(err, "cannot generate code from buf")
	}
	err = WriteHash(ctx, hashfile(g.Dir), hash)
	if err != nil {
		return w.Wrapf(err, "cannot write hash")
	}
	return nil
}

func WriteHash(_ context.Context, hashFile string, hash string) error {
	// New or overwrite
	f, err := os.Create(hashFile)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "%s", hash)
	if err != nil {
		return err
	}
	return nil
}
