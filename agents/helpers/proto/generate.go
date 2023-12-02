package proto

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
)

type Proto struct {
	Dir string
}

func (g *Proto) Generate(ctx context.Context) error {
	logger := shared.AgentLogger(ctx)
	version, err := configurations.Version()
	if err != nil {
		return logger.Wrapf(err, "cannot get version")
	}
	image := fmt.Sprintf("codefly/companion:%s", version)
	volume := fmt.Sprintf("%s:/workspace", g.Dir)
	logger.DebugMe("VOLUME %v", volume)
	cmd := exec.Command("docker", "run", "--rm", "-v", volume, image, "buf", "generate")
	cmd.Dir = g.Dir
	logger.Debugf("Generating code from buf...")
	output, err := cmd.CombinedOutput()
	if err != nil {
		return logger.Wrapf(err, "cannot generate code from buf: %v", string(output))
	}
	return nil
}
