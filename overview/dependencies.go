package overview

import (
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/shared"
)

/*
Overview builds a dependency graph of the application and its services.
*/

type DependencyGraph struct {
	*Graph
}

func (g *DependencyGraph) Nodes() []string {
	var nodes []string
	for node := range g.Graph.Nodes {
		nodes = append(nodes, node)
	}
	return nodes
}

type Edge struct {
	From string
	To   string
}

func (g *DependencyGraph) Edges() []Edge {
	var edges []Edge
	for from, tos := range g.Graph.Edges {
		for _, to := range tos {
			edges = append(edges, Edge{
				From: from,
				To:   to,
			})
		}
	}
	return edges
}

func NewDependencyGraph(project *configurations.Project) (*DependencyGraph, error) {
	logger := shared.NewLogger().With("overview.NewDependencyGraph")
	graph, err := Load(project)
	if err != nil {
		return nil, logger.Wrapf(err, "cannot load graph")
	}
	logger.DebugMe("loaded graph: %v %v", graph.Nodes, graph.Edges)
	return &DependencyGraph{
		Graph: graph,
	}, nil
}

func Load(project *configurations.Project) (*Graph, error) {
	//logger := shared.NewLogger().With("overview.Load")
	//graph := NewGraph(project.Name)
	//for _, appRef := range project.Applications {
	//	app, err := project.LoadApplicationFromReference(appRef)
	//	if err != nil {
	//		return nil, logger.Wrapf(err, "cannot load application <%s>", appRef.Name)
	//	}
	//	for _, serviceRef := range app.Services {
	//		service, err := app.LoadServiceFromReference(serviceRef, configurations.WithProject(project))
	//		if err != nil {
	//			return nil, logger.Wrapf(err, "cannot load service <%s>", serviceRef.Name)
	//		}
	//		graph.AddNode(service.Unique())
	//		for _, dep := range service.Dependencies {
	//			graph.AddNode(dep.Unique())
	//			graph.AddEdge(dep.Unique(), service.Unique())
	//		}
	//	}
	//}
	//return graph, nil
	return nil, nil
}
