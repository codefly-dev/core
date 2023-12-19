package architecture_test

import (
	"context"
	"testing"

	"github.com/codefly-dev/core/architecture"
	"github.com/codefly-dev/core/configurations"
	"github.com/stretchr/testify/assert"
)

func TestGraph(t *testing.T) {
	ctx := context.Background()
	ws := &configurations.Workspace{}
	project, err := ws.LoadProjectFromDir(ctx, "testdata/codefly-platform")
	assert.NoError(t, err)
	assert.NotNil(t, project)
	assert.Equal(t, 2, len(project.Applications))
	g, err := architecture.NewDependencyGraph(ctx, project)
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

	expectedWebEdge := &architecture.Edge{
		From: "web/gateway", // is a dependency for
		To:   "web/frontend",
	}
	expectedManagementEdge := &architecture.Edge{
		From: "management/organization", // is a dependency for
		To:   "web/gateway",
	}
	for _, expected := range []*architecture.Edge{expectedWebEdge, expectedManagementEdge} {
		found := false
		for _, edge := range g.ServiceDependencyGraph.Edges() {
			if edge.From == expected.From && edge.To == expected.To {
				found = true
			}
		}
		assert.True(t, found)
	}
}
