package overview_test

import (
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/overview"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestGraph(t *testing.T) {
	configurations.OverrideWorkspaceProjectRoot(configurations.SolveDir("testdata"))
	project, err := configurations.LoadProjectFromName("codefly-platform")
	assert.NoError(t, err)
	assert.NotNil(t, project)
	depGraph, err := overview.NewDependencyGraph(project)
	assert.NoError(t, err)
	assert.NotNil(t, depGraph)
	// should find a node from web/frontend to web/gateway
	// and from web/gateway to management/organization
	edges := depGraph.Edges()
	assert.Equal(t, 2, len(edges))
	expectedWebEdge := &overview.Edge{
		From: "web/gateway", // is a dependency for
		To:   "web/frontend",
	}
	expectedManagementEdge := &overview.Edge{
		From: "management/organization", // is a dependency for
		To:   "web/gateway",
	}
	for _, expected := range []*overview.Edge{expectedWebEdge, expectedManagementEdge} {
		found := false
		for _, edge := range edges {
			if edge.From == expected.From && edge.To == expected.To {
				found = true
			}
		}
		assert.True(t, found)
	}
}
