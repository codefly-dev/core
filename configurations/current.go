package configurations

import (
	"context"

	"github.com/codefly-dev/core/shared"
)

func ActiveDefaultProject(ctx context.Context) (*Project, error) {
	logger := shared.GetLogger(ctx).With("ActiveDefaultProject")
	ws, err := LoadWorkspace(ctx)
	if err != nil {
		return nil, logger.Wrap(err)
	}
	return ws.LoadActiveProject(ctx)
}
