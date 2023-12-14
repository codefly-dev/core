package golang

import (
	"context"
	"os/exec"

	"github.com/codefly-dev/core/shared"
)

func (g *Go) ModTidy(ctx context.Context) error {
	logger := shared.GetLogger(ctx).With("go.Update")
	logger.Info("Tidying go modules...")
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = g.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return logger.Wrapf(err, "cannot run go mod tidy: %s", string(out))
	}
	return nil
}

func (g *Go) Update(ctx context.Context) error {
	logger := shared.GetLogger(ctx).With("go.Update")
	logger.Info("Updating go modules...")
	cmd := exec.Command("go", "get", "-u", "./...")
	cmd.Dir = g.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return logger.Wrapf(err, "cannot run go get -u: %s", string(out))
	}
	return nil
}
