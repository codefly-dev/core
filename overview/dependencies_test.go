package overview_test

import (
	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/overview"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestGraph(t *testing.T) {
	project, err := configurations.LoadProjectFromDir(configurations.SolveDir("testdata/codefly-platform"))
	assert.NoError(t, err)
	assert.NotNil(t, project)
	assert.Equal(t, 2, len(project.Applications))
	depGraph, err := overview.NewDependencyGraph(project)
	assert.NoError(t, err)
	assert.NotNil(t, depGraph)
	// should find a node from web/frontend to web/gateway
	// and from web/gateway to management/organization
	assert.Equal(t, 3, len(depGraph.Nodes()))
	for _, node := range depGraph.Nodes() {
		t.Logf("node: %v", node)
	}
	edges := depGraph.Edges()
	for _, ed := range edges {
		t.Logf("edge: %v", ed)
	}
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
