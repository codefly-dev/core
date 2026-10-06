package architecture

import (
	"context"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"
)

// LoadPublicModuleGraph builds the cross-module endpoint graph: the endpoints
// that cross each module's boundary, by REACH. A module's declared interface is
// its export boundary: when one is present only the endpoints it declares are
// graphed and any other endpoint, however it declares itself, is invisible to
// other modules. A module without an interface graphs every endpoint whose own
// visibility crosses module lines (internal or public). Whether an endpoint is
// addressed from outside the workspace is exposure, and no part of this graph.
func LoadPublicModuleGraph(ctx context.Context, workspace *resources.Workspace) ([]*DAG, error) {
	w := wool.Get(ctx).In("LoadModuleGraph")
	var gs []*DAG
	for _, modRef := range workspace.Modules {
		mod, err := workspace.LoadModuleFromReference(ctx, modRef)
		if err != nil {
			return nil, w.With(wool.NameField(modRef.Name)).Wrapf(err, "cannot load module")
		}
		endpoints, err := mod.ExportedEndpoints(ctx)
		if err != nil {
			return nil, w.With(wool.NameField(modRef.Name)).Wrapf(err, "cannot load exported endpoints")
		}
		if len(endpoints) == 0 {
			continue
		}
		g := NewDAG(mod.Name)
		g.AddNode(mod.Unique()).WithType(resources.MODULE)
		// Add one edge for each of the service endpoint
		for _, endpoint := range endpoints {
			service := resources.ServiceUnique(mod.Name, endpoint.Service)
			g.AddNode(service).WithType(resources.SERVICE)
			g.AddEdge(mod.Unique(), service)
			e := resources.EndpointFromProto(endpoint)
			g.AddNode(e.Unique()).WithType(resources.ENDPOINT)
			g.AddEdge(service, e.Unique())
		}
		gs = append(gs, g)

	}
	return gs, nil
}
