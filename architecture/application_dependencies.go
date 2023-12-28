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
		g.AddNode(app.Unique(), configurations.APPLICATION)
		// Add one edge for each of the service endpoint
		for _, serviceEndpointGroup := range group.ServiceEndpointGroups {

			g.AddNode(serviceEndpointGroup.Name, configurations.SERVICE)
			g.AddEdge(app.Unique(), serviceEndpointGroup.Name)
			for _, endpoint := range serviceEndpointGroup.Endpoints {
				e, err := configurations.FromProtoEndpoint(endpoint)
				if err != nil {
					return nil, w.With(wool.NameField(appRef.Name)).Wrapf(err, "cannot convert endpoint")
				}
				g.AddNode(e.Unique(), configurations.ENDPOINT)
				g.AddEdge(serviceEndpointGroup.Name, e.Unique())
			}
		}
		gs = append(gs, g)

	}
	return gs, nil
}
