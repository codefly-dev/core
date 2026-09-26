package composition

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/resources"
)

// The published definitions the resources package tests against; a host
// would resolve them from the owners' releases.
const (
	reportWidgetsDir = "../resources/testdata/interfaces/widgets-1.2.0"
	reportCacheDir   = "../resources/testdata/interfaces/cache-0.3.0"
)

func reportDefinition(t *testing.T, dir, version string, change func(*resources.Interface)) *resources.Interface {
	t.Helper()
	definition, err := resources.LoadInterfaceFromDir(context.Background(), dir)
	require.NoError(t, err)
	definition.Version = version
	if change != nil {
		change(definition)
	}
	require.NoError(t, definition.Validate())
	return definition
}

func TestSemanticReportComputesInterfaceCompatibility(t *testing.T) {
	addMethod := func(i *resources.Interface) {
		i.Protobuf.Services[0].Methods = append(i.Protobuf.Services[0].Methods, "DeleteWidget")
	}
	removeMethod := func(i *resources.Interface) { i.Protobuf.Services[0].Methods = i.Protobuf.Services[0].Methods[:1] }
	before := []*resources.Interface{
		reportDefinition(t, reportWidgetsDir, "1.2.0", nil),
		reportDefinition(t, reportCacheDir, "0.3.0", nil),
	}

	report := &SemanticReport{}
	report.AddInterfaceEvolutions(before, []*resources.Interface{
		reportDefinition(t, reportWidgetsDir, "1.3.0", addMethod),
		reportDefinition(t, reportCacheDir, "0.3.0", nil),
	})
	require.Empty(t, report.BlockedReasons, "an additive minor and an unchanged interface are safe")
	require.Len(t, report.Interfaces, 2)
	require.Contains(t, report.String(), "example.dev/widgets")

	// The author's version cannot hide what the surface shows.
	report = &SemanticReport{}
	report.AddInterfaceEvolutions(before[:1], []*resources.Interface{reportDefinition(t, reportWidgetsDir, "1.3.0", removeMethod)})
	require.Len(t, report.BlockedReasons, 1)
	require.Contains(t, report.BlockedReasons[0], "1.3.0 is not a breaking version of 1.2.0")

	// A declared break is honest, but consumers of ^1.2 lose their provider.
	report = &SemanticReport{}
	report.AddInterfaceEvolutions(before[:1], []*resources.Interface{reportDefinition(t, reportWidgetsDir, "2.0.0", removeMethod)})
	require.Equal(t, []string{"interface example.dev/widgets@1.2.0 is no longer implemented"}, report.BlockedReasons)

	// Implementing the next line beside the current one breaks nobody.
	report = &SemanticReport{}
	report.AddInterfaceEvolutions(before[:1], []*resources.Interface{
		reportDefinition(t, reportWidgetsDir, "1.2.0", nil),
		reportDefinition(t, reportWidgetsDir, "2.0.0", removeMethod),
	})
	require.Empty(t, report.BlockedReasons)
	require.Equal(t, []string{"newly implemented"}, report.Interfaces[1].Additive)
}
