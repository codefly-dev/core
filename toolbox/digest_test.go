package toolbox_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/toolbox"
	"github.com/codefly-dev/core/toolbox/conformance"
)

func TestCatalogDigestIsCanonicalAndSensitiveToContract(t *testing.T) {
	server := conformance.New(conformance.FixtureVersion)
	snapshot, err := toolbox.SnapshotServer(context.Background(), server)
	require.NoError(t, err)
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, snapshot.Digest)
	require.Equal(t, []string{
		conformance.EffectCountTool,
		conformance.EffectIncrementTool,
		conformance.DeterministicErrorTool,
		conformance.IdentityTool,
		conformance.CrashTool,
		conformance.WaitTool,
	}, snapshot.ToolNames())

	reversedSummaries := proto.Clone(snapshot.Summaries).(*toolboxv0.ListToolSummariesResponse)
	for left, right := 0, len(reversedSummaries.Tools)-1; left < right; left, right = left+1, right-1 {
		reversedSummaries.Tools[left], reversedSummaries.Tools[right] = reversedSummaries.Tools[right], reversedSummaries.Tools[left]
	}
	reordered, err := toolbox.NewCatalogSnapshot(snapshot.Identity, reversedSummaries)
	require.NoError(t, err)
	require.Equal(t, snapshot.Digest, reordered.Digest, "wire ordering must not change catalog identity")

	description, err := server.DescribeTool(context.Background(), &toolboxv0.DescribeToolRequest{Name: conformance.IdentityTool})
	require.NoError(t, err)
	approved, err := snapshot.ApproveTool(conformance.IdentityTool, description)
	require.NoError(t, err)
	changedDescription := proto.Clone(description).(*toolboxv0.DescribeToolResponse)
	changedDescription.Tool.Description += " changed"
	changed, err := snapshot.ApproveTool(conformance.IdentityTool, changedDescription)
	require.NoError(t, err)
	require.NotEqual(t, approved.Digest, changed.Digest)
}

func TestCallToolRequestDigestBindsArgumentsAndRoots(t *testing.T) {
	arguments, err := structpb.NewStruct(map[string]any{"subject": "one"})
	require.NoError(t, err)
	request := &toolboxv0.CallToolRequest{
		Name: conformance.IdentityTool, Arguments: arguments, Roots: []string{"file:///workspace"},
	}
	first, err := toolbox.DigestCallToolRequest(request)
	require.NoError(t, err)
	second, err := toolbox.DigestCallToolRequest(proto.Clone(request).(*toolboxv0.CallToolRequest))
	require.NoError(t, err)
	require.Equal(t, first, second)

	changedArgs := proto.Clone(request).(*toolboxv0.CallToolRequest)
	changedArgs.Arguments.Fields["subject"], _ = structpb.NewValue("two")
	changedArgsDigest, err := toolbox.DigestCallToolRequest(changedArgs)
	require.NoError(t, err)
	require.NotEqual(t, first, changedArgsDigest)

	changedRoots := proto.Clone(request).(*toolboxv0.CallToolRequest)
	changedRoots.Roots = []string{"file:///other"}
	changedRootsDigest, err := toolbox.DigestCallToolRequest(changedRoots)
	require.NoError(t, err)
	require.NotEqual(t, first, changedRootsDigest)
}

func TestCatalogSnapshotRejectsMalformedDiscovery(t *testing.T) {
	identity := &toolboxv0.IdentityResponse{Name: "x", Version: "1"}
	summaries := &toolboxv0.ListToolSummariesResponse{Tools: []*toolboxv0.ToolSummary{{Name: "x.read"}}}
	snapshot, err := toolbox.NewCatalogSnapshot(identity, summaries)
	require.NoError(t, err)

	descriptions := []*toolboxv0.DescribeToolResponse{{
		Tool: &toolboxv0.ToolSpec{Name: "other.read"},
	}}
	_, err = snapshot.ApproveTool("x.read", descriptions[0])
	require.ErrorContains(t, err, "requested tool")
}

func TestCatalogApprovalRejectsSummaryClassificationDriftAndFreeTextIdempotency(t *testing.T) {
	server := conformance.New(conformance.FixtureVersion)
	snapshot, err := toolbox.SnapshotServer(context.Background(), server)
	require.NoError(t, err)
	description, err := server.DescribeTool(context.Background(), &toolboxv0.DescribeToolRequest{Name: conformance.IdentityTool})
	require.NoError(t, err)

	drifted := proto.Clone(description).(*toolboxv0.DescribeToolResponse)
	drifted.Tool.Destructive = true
	_, err = snapshot.ApproveTool(conformance.IdentityTool, drifted)
	require.ErrorContains(t, err, "destructive classification")

	freeText := proto.Clone(description).(*toolboxv0.DescribeToolResponse)
	freeText.Tool.Idempotency = "probably safe"
	_, err = snapshot.ApproveTool(conformance.IdentityTool, freeText)
	require.ErrorContains(t, err, "unsupported idempotency")
}
