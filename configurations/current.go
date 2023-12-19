package configurations

import (
	"context"

	wool "github.com/codefly-dev/core/wool"
)

func ActiveDefaultProject(ctx context.Context) (*Project, error) {
	w := wool.Get(ctx).In("ActiveDefaultProject")
	ws, err := LoadWorkspace(ctx)
	if err != nil {
		return nil, w.Wrap(err)
	}
	return ws.LoadActiveProject(ctx)
}
