package architecture

import (
	"context"

	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/configurations"
)

/*
Overview builds a dependency graph of the application and its services.
*/

func LoadPublicApplicationGraph(ctx context.Context, project *configurations.Project) ([]*Graph, error) {
	w := wool.Get(ctx).In("LoadApplicationGraph")
	//graph := NewGraph(project.Name)
	var gs []*Graph
	for _, appRef := range project.Applications {
		app, err := project.LoadApplicationFromReference(ctx, appRef)
		if err != nil {
			return nil, w.With(wool.NameField(appRef.Name)).Wrapf(err, "cannot load application")
		}
		group, err := app.PublicEndpoints(ctx)
		if err != nil {
			return nil, w.With(wool.NameField(appRef.Name)).Wrapf(err, "cannot load public endpoints")
		}
		if group == nil {
			continue
		}
		g := NewGraph(app.Name)
		g.AddNode(app.Unique())
		// Add one edge for each of the service endpoint
		for _, serviceEndpoint := range group.ServiceEndpointGroups {

			g.AddNode(serviceEndpoint.Name)
			g.AddEdge(app.Unique(), serviceEndpoint.Name)
			for _, endpoint := range serviceEndpoint.Endpoints {
				e, err := configurations.FromProtoEndpoint(endpoint)
				if err != nil {
					return nil, w.With(wool.NameField(appRef.Name)).Wrapf(err, "cannot convert endpoint")
				}
				g.AddNode(e.Unique())
				g.AddEdge(serviceEndpoint.Name, e.Unique())
			}
		}
		gs = append(gs, g)

	}
	return gs, nil
}
