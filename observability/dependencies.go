package observability

import (
	"context"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
)

/*
Overview builds a dependency graph of the application and its services.
*/

type DependencyGraph struct {
	ServiceDependencyGraph *Graph
}

func NewDependencyGraph(ctx context.Context, project *configurations.Project) (*DependencyGraph, error) {
	logger := shared.NewLogger().With("overview.NewDependencyGraph")
	serviceGraph, err := LoadServiceGraph(ctx, project)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load graph")
	}
	return &DependencyGraph{
		ServiceDependencyGraph: serviceGraph,
	}, nil
}

func LoadServiceGraph(ctx context.Context, project *configurations.Project) (*Graph, error) {
	logger := shared.NewLogger().With("overview.LoadServiceGraph")
	graph := NewGraph(project.Name)
	for _, appRef := range project.Applications {
		app, err := project.LoadApplicationFromReference(ctx, appRef)
		if err != nil {
			return nil, logger.Wrapf(err, "cannot load application <%s>", appRef.Name)
		}
		for _, serviceRef := range app.Services {
			service, err := app.LoadServiceFromReference(ctx, serviceRef)
			if err != nil {
				return nil, logger.Wrapf(err, "cannot load service <%s>", serviceRef.Name)
			}
			graph.AddNode(service.Unique())
			for _, dep := range service.Dependencies {
				graph.AddNode(dep.Unique())
				graph.AddEdge(dep.Unique(), service.Unique())
			}
		}
	}
	return graph, nil
}