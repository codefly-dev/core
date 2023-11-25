package golang

import (
	"os/exec"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/helpers/setup"
)

func (g *Go) BufGenerate(logger *agents.AgentLogger) error {
	if !setup.Has("buf") {
		return setup.NewMissingSoftwareError("buf")
	}
	cmd := exec.Command("buf", "mod", "update")
	cmd.Dir = g.Dir
	logger.Info("Preparing for code generation...")
	err := cmd.Run()
	if err != nil {
		return err
	}
	cmd = exec.Command("buf", "generate")
	cmd.Dir = g.Dir
	logger.Info("Generating code...")
	return cmd.Run()
}
