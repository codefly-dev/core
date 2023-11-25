package golang

import (
	"os/exec"

	"github.com/codefly-dev/core/agents"
)

func (g *Go) ModTidy(logger *agents.AgentLogger) error {
	logger.Info("Tidying go modules...")
	cmd := exec.Command("go", "mod", "tidy")
	cmd.Dir = g.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return logger.Wrapf(err, "cannot run go mod tidy: %s", string(out))
	}
	return nil
}

func (g *Go) Update(logger *agents.AgentLogger) error {
	logger.Info("Updating go modules...")
	cmd := exec.Command("go", "get", "-u", "./...")
	cmd.Dir = g.Dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return logger.Wrapf(err, "cannot run go get -u: %s", string(out))
	}
	return nil
}
