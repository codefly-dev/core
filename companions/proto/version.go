package proto

import (
	"context"
	"embed"
	"fmt"
	"io/fs"

	"gopkg.in/yaml.v3"

	"github.com/Masterminds/semver"
	"github.com/codefly-dev/core/wool"
)

func version(ctx context.Context) (string, error) {
	w := wool.Get(ctx).In("proto.version")

	content, err := fs.ReadFile(infoFS, "info.codefly.yaml")
	if err != nil {
		return "", w.Wrapf(err, "cannot read file")
	}
	var info Info
	if err = yaml.Unmarshal(content, &info); err != nil {
		return "", w.Wrapf(err, "cannot unmarshal file")
	}
	// check we have a valid semantic version
	v, err := semver.NewVersion(info.Version)
	if err != nil {
		return "", w.Wrapf(err, "cannot parse version <%s>", info.Version)
	}
	return v.String(), nil
}

func CompanionImage(ctx context.Context) (string, error) {
	w := wool.Get(ctx).In("proto.CompanionImage")
	v, err := version(ctx)
	if err != nil {
		return "", w.Wrapf(err, "cannot get version")
	}
	return fmt.Sprintf("codeflydev/companion:%s", v), nil
}

//go:embed info.codefly.yaml
var infoFS embed.FS
