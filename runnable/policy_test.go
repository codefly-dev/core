package runnable_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/runnable"
)

// The seam a hand-copying writer loses fields on: what an owner declared,
// derived into a spec, written into a binding and read back by a host, is the
// declaration — tool exposure included — on both the gRPC and the REST form.
func TestPolicyCarriesADerivedSpecIntoAPreparedBinding(t *testing.T) {
	declared := declaredOperation()
	declared.Tool = exposedTool()
	_, spec, err := runnable.PackageFromMethod(ingestionFiles(t, declared), ingestLocation(), ingestOwner(), applyText)
	require.NoError(t, err)

	binding := connectBinding()
	binding.Operation.Spelling = applyText
	binding.Call.Route = &runnablev0.PreparedCall_Connect{Connect: &runnablev0.ConnectProcedure{Procedure: applyText}}
	binding.Policy = spec.Policy()
	delivered, err := runnable.DecodePrepared(encoded(t, binding))
	require.NoError(t, err)
	require.True(t, proto.Equal(declared, delivered.GetPolicy()), "the binding delivers what the owner declared:\n%v\n%v", declared, delivered.GetPolicy())
	tool, err := runnable.ToolFromPrepared(delivered)
	require.NoError(t, err)
	require.True(t, proto.Equal(exposedTool(), tool))

	// The OpenAPI marker is the same schema with transport-specific codes.
	declared.LookupMethod = ""
	declared.RetryableCodes = []string{"503"}
	marker, err := protojson.Marshal(declared)
	require.NoError(t, err)
	restSpec, err := runnable.OperationFromOpenAPIMarker(marker, restSpelling)
	require.NoError(t, err)
	rest := restBinding()
	rest.Policy = restSpec.Policy()
	delivered, err = runnable.DecodePrepared(encoded(t, rest))
	require.NoError(t, err)
	require.True(t, proto.Equal(declared, delivered.GetPolicy()), "the REST binding delivers what the marker declared:\n%v\n%v", declared, delivered.GetPolicy())
	tool, err = runnable.ToolFromPrepared(delivered)
	require.NoError(t, err)
	require.True(t, proto.Equal(exposedTool(), tool))
}
