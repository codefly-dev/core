package conformance_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox/conformance"
)

type protocolGolden struct {
	CanonicalToolNames  []string `json:"canonical_tool_names"`
	Identity            any      `json:"identity"`
	Summaries           any      `json:"summaries"`
	IdentityDescription any      `json:"identity_description"`
	LegacyTools         any      `json:"legacy_tools"`
}

func projectedJSON(t *testing.T, message proto.Message) any {
	t.Helper()
	encoded, err := (protojson.MarshalOptions{
		UseProtoNames:   true,
		EmitUnpopulated: true,
	}).Marshal(message)
	require.NoError(t, err)
	var projected any
	require.NoError(t, json.Unmarshal(encoded, &projected))
	return projected
}

func TestFixtureProtocolProjectionGolden(t *testing.T) {
	ctx := context.Background()
	server := conformance.New(conformance.FixtureVersion)
	identity, err := server.Identity(ctx, &toolboxv0.IdentityRequest{})
	require.NoError(t, err)
	summaries, err := server.ListToolSummaries(ctx, &toolboxv0.ListToolSummariesRequest{})
	require.NoError(t, err)
	description, err := server.DescribeTool(ctx, &toolboxv0.DescribeToolRequest{Name: conformance.IdentityTool})
	require.NoError(t, err)
	legacy, err := server.ListTools(ctx, &toolboxv0.ListToolsRequest{})
	require.NoError(t, err)

	names := make([]string, 0, len(summaries.Tools))
	for _, summary := range summaries.Tools {
		names = append(names, summary.Name)
	}
	actual, err := json.MarshalIndent(protocolGolden{
		CanonicalToolNames:  names,
		Identity:            projectedJSON(t, identity),
		Summaries:           projectedJSON(t, summaries),
		IdentityDescription: projectedJSON(t, description),
		LegacyTools:         projectedJSON(t, legacy),
	}, "", "  ")
	require.NoError(t, err)
	actual = append(actual, '\n')

	expected, err := os.ReadFile("testdata/protocol.golden.json")
	if err != nil {
		t.Fatalf("read protocol golden: %v\nactual:\n%s", err, actual)
	}
	require.Equal(t, string(expected), string(actual),
		"intentional Toolbox contract changes must update the reviewed protocol golden")
}
