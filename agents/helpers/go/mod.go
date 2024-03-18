package golang

import (
	"context"
	"os/exec"
	"path"

	"github.com/codefly-dev/core/shared"

	"github.com/codefly-dev/core/wool"
)

func (g *Go) ModTidy(ctx context.Context) error {
	w := wool.Get(ctx).In("go.ModTidy")
	if !shared.FileExists(path.Join(g.Dir, "go.mod")) {
		return nil
	}
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = g.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return w.Wrapf(err, "cannot run go mod tidy: %s", string(out))
	}
	return nil
}

func (g *Go) ModDowload(ctx context.Context) error {
	w := wool.Get(ctx).In("go.ModTidy")
	if !shared.FileExists(path.Join(g.Dir, "go.mod")) {
		return nil
	}
	cmd := exec.Command("go", "mod", "download")
	cmd.Dir = g.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return w.Wrapf(err, "cannot run go mod download: %s", string(out))
	}
	return nil
}

func (g *Go) Update(ctx context.Context) error {
	w := wool.Get(ctx).In("go.Update")
	w.Info("Updating go modules...")
	cmd := exec.Command("go", "get", "-u", "./...")
	cmd.Dir = g.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return w.Wrapf(err, "cannot run go get -u: %s", string(out))
	}
	return nil
}
