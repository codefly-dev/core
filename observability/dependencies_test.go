package observability_test

import (
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/observability"
	"github.com/codefly-dev/core/shared"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestGraph(t *testing.T) {
	ctx := shared.NewContext()
	ws := &configurations.Workspace{}
	project, err := ws.LoadProjectFromDir(ctx, "testdata/codefly-platform")
	assert.NoError(t, err)
	assert.NotNil(t, project)
	assert.Equal(t, 2, len(project.Applications))
	g, err := observability.NewDependencyGraph(ctx, project)
	assert.NoError(t, err)
	assert.NotNil(t, g)

	// applications:
	// management:
	// - organization
	// web:
	// - frontend -> gateway
	// - gateway -> organization
	assert.Equal(t, 3, len(g.ServiceDependencyGraph.Nodes()))
	
	assert.Equal(t, 2, len(g.ServiceDependencyGraph.Edges()))

	expectedWebEdge := &observability.Edge{
		From: "web/gateway", // is a dependency for
		To:   "web/frontend",
	}
	expectedManagementEdge := &observability.Edge{
		From: "management/organization", // is a dependency for
		To:   "web/gateway",
	}
	for _, expected := range []*observability.Edge{expectedWebEdge, expectedManagementEdge} {
		found := false
		for _, edge := range g.ServiceDependencyGraph.Edges() {
			if edge.From == expected.From && edge.To == expected.To {
				found = true
			}
		}
		assert.True(t, found)
	}
}
