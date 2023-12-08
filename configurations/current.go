package configurations

import (
	"context"

	"github.com/codefly-dev/core/shared"
)

func ActiveDefaultProject(ctx context.Context) (*Project, error) {
	logger := shared.GetBaseLogger(ctx).With("ActiveDefaultProject")
	ws, err := ActiveWorkspace(ctx)
	if err != nil {
		return nil, logger.Wrap(err)
	}
	return ws.LoadActiveProject(ctx)
}
