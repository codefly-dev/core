package runnable_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/durationpb"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runnable"
)

const (
	connectSpelling = "/acme.items.v1.Items/Apply"
	restSpelling    = "POST /v1/items"
)

func preparedPolicy() *runnablev0.Operation {
	return &runnablev0.Operation{
		AttemptTimeout: durationpb.New(10 * time.Second),
		TotalTimeout:   durationpb.New(time.Minute),
		MaxAttempts:    3,
		Backoff:        durationpb.New(time.Second),
		RetryableCodes: []string{"UNAVAILABLE"},
		Audience:       "acme.items",
		InvokeScopes:   []*basev0.WorkScopeV1{{ResourceKind: "acme.item", Actions: []string{"read", "write"}}},
		LookupScopes:   []*basev0.WorkScopeV1{{ResourceKind: "acme.item", Actions: []string{"read"}}},
	}
}

func preparedContract() *basev0.RunnableContract {
	return &basev0.RunnableContract{
		Protocol: resources.RunnableServiceProtocolV1,
		Input:    &basev0.RunnableSchema{Fields: []*basev0.RunnableField{{Name: "item", Type: basev0.RunnableField_STRING}}},
		Output:   &basev0.RunnableSchema{Fields: []*basev0.RunnableField{{Name: "applied", Type: basev0.RunnableField_BOOLEAN}}},
	}
}

func connectBinding() *runnablev0.PreparedBinding {
	return &runnablev0.PreparedBinding{
		Operation: &runnablev0.PreparedOperation{Module: "owner", Service: "items", Endpoint: "grpc", Spelling: connectSpelling},
		Call: &runnablev0.PreparedCall{
			Address: "items-grpc.owner:9090",
			Route:   &runnablev0.PreparedCall_Connect{Connect: &runnablev0.ConnectProcedure{Procedure: connectSpelling}},
		},
		Contract: preparedContract(),
		Policy:   preparedPolicy(),
	}
}

func restBinding() *runnablev0.PreparedBinding {
	binding := connectBinding()
	binding.Operation.Endpoint = "rest"
	binding.Operation.Spelling = restSpelling
	binding.Call.Route = &runnablev0.PreparedCall_Rest{Rest: &runnablev0.HTTPRoute{Verb: "POST", Path: "/v1/items"}}
	// A REST operation names the outcomes it retries by HTTP status.
	binding.Policy.RetryableCodes = []string{"503"}
	return binding
}

func encoded(t *testing.T, binding *runnablev0.PreparedBinding) []byte {
	t.Helper()
	value, err := runnable.EncodePrepared(binding)
	require.NoError(t, err)
	return value
}

func TestAPreparedBindingRoundTripsAndCarriesNoDescriptors(t *testing.T) {
	value := encoded(t, connectBinding())

	decoded, err := runnable.DecodePrepared(value)
	require.NoError(t, err)
	require.Equal(t, runnable.PreparedSchemaV3, decoded.GetSchema())
	require.Equal(t, connectSpelling, decoded.GetCall().GetConnect().GetProcedure())
	require.Equal(t, "items-grpc.owner:9090", decoded.GetCall().GetAddress())
	require.True(t, proto.Equal(preparedContract(), decoded.GetContract()))
	require.True(t, proto.Equal(preparedPolicy(), decoded.GetPolicy()))

	digest, err := runnable.ContractDigest(preparedContract())
	require.NoError(t, err)
	require.Equal(t, digest, decoded.GetContractDigest())

	// The size claim the design rests on: what a caller installs is the
	// operation, the call, the bounded contract and the policy.
	require.Less(t, len(value), 1024, string(value))
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(value, &fields))
	require.NotContains(t, fields, "descriptors")
	require.NotContains(t, fields, "descriptor_set")
	require.NotContains(t, fields, "package")
	require.NotContains(t, fields, "binding")
}

func TestARESTOperationIsPreparedOnItsOwnRoute(t *testing.T) {
	decoded, err := runnable.DecodePrepared(encoded(t, restBinding()))
	require.NoError(t, err)
	require.Equal(t, restSpelling, decoded.GetOperation().GetSpelling())
	require.Equal(t, "POST", decoded.GetCall().GetRest().GetVerb())
	require.Equal(t, "/v1/items", decoded.GetCall().GetRest().GetPath())
	require.Equal(t, restSpelling, runnable.Route(decoded.GetCall().GetRest().GetVerb(), decoded.GetCall().GetRest().GetPath()))
}

func TestAPreparedBindingsRouteMustBeTheOperationItNames(t *testing.T) {
	elsewhere := connectBinding()
	elsewhere.Call.Route = &runnablev0.PreparedCall_Connect{Connect: &runnablev0.ConnectProcedure{Procedure: "/acme.items.v1.Items/Cancel"}}
	_, err := runnable.EncodePrepared(elsewhere)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.Contains(t, err.Error(), "/acme.items.v1.Items/Cancel")

	otherRoute := restBinding()
	otherRoute.Call.Route = &runnablev0.PreparedCall_Rest{Rest: &runnablev0.HTTPRoute{Verb: "PUT", Path: "/v1/items"}}
	_, err = runnable.EncodePrepared(otherRoute)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.Contains(t, err.Error(), "PUT /v1/items")

	// A gRPC spelling read as a route, and a route read as a procedure: the
	// conflation the typed target replaces.
	asProcedure := restBinding()
	asProcedure.Call.Route = &runnablev0.PreparedCall_Connect{Connect: &runnablev0.ConnectProcedure{Procedure: restSpelling}}
	_, err = runnable.EncodePrepared(asProcedure)
	require.ErrorIs(t, err, runnable.ErrInvalid)

	noRoute := connectBinding()
	noRoute.Call.Route = nil
	_, err = runnable.EncodePrepared(noRoute)
	require.ErrorIs(t, err, runnable.ErrInvalid)
}

func TestARetryPolicyIsReadInItsTransportsVocabulary(t *testing.T) {
	httpOnConnect := connectBinding()
	httpOnConnect.Policy.RetryableCodes = []string{"503"}
	_, err := runnable.EncodePrepared(httpOnConnect)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.Contains(t, err.Error(), "gRPC status code name")

	grpcOnRest := restBinding()
	grpcOnRest.Policy.RetryableCodes = []string{"UNAVAILABLE"}
	_, err = runnable.EncodePrepared(grpcOnRest)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.Contains(t, err.Error(), "HTTP status")
}

func TestAPolicyOutsideTheInstallationBoundsIsRefused(t *testing.T) {
	tooManyAttempts := connectBinding()
	tooManyAttempts.Policy.MaxAttempts = runnable.MaxOperationAttempts + 1
	_, err := runnable.EncodePrepared(tooManyAttempts)
	require.ErrorIs(t, err, runnable.ErrInvalid)

	noAuthority := connectBinding()
	noAuthority.Policy.Audience = ""
	_, err = runnable.EncodePrepared(noAuthority)
	require.ErrorIs(t, err, runnable.ErrInvalid)

	wideningLookup := connectBinding()
	wideningLookup.Policy.LookupScopes = []*basev0.WorkScopeV1{{ResourceKind: "acme.item", Actions: []string{"write"}}}
	_, err = runnable.EncodePrepared(wideningLookup)
	require.ErrorIs(t, err, runnable.ErrInvalid)
}

func TestADeliveredValueIsHeldToItsContractDigest(t *testing.T) {
	stated := connectBinding()
	stated.ContractDigest = "sha256:" + strings.Repeat("0", 64)
	_, err := runnable.EncodePrepared(stated)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.Contains(t, err.Error(), "derives")

	// An owner that republishes a changed contract derives another digest, and
	// the value delivered for the contract it used to publish is refused
	// rather than called with a payload shaped for it.
	drifted := connectBinding()
	drifted.ContractDigest = ""
	value := encoded(t, drifted)
	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(value, &fields))
	fields["contract"] = json.RawMessage(`{"protocol":"codefly.runnable.service/v1","input":{"fields":[{"name":"item","type":"STRING"},{"name":"revision","type":"INTEGER"}]},"output":{"fields":[{"name":"applied","type":"BOOLEAN"}]}}`)
	tampered, err := json.Marshal(fields)
	require.NoError(t, err)
	_, err = runnable.DecodePrepared(tampered)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.Contains(t, err.Error(), "contract digest")
}

func TestAPreparedValueIsReadStrictly(t *testing.T) {
	value := encoded(t, connectBinding())

	var fields map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(value, &fields))

	unknown := map[string]json.RawMessage{}
	for key, raw := range fields {
		unknown[key] = raw
	}
	unknown["descriptors"] = json.RawMessage(`"AAAA"`)
	withUnknown, err := json.Marshal(unknown)
	require.NoError(t, err)
	_, err = runnable.DecodePrepared(withUnknown)
	require.ErrorIs(t, err, runnable.ErrInvalid)

	otherSchema := map[string]json.RawMessage{}
	for key, raw := range fields {
		otherSchema[key] = raw
	}
	otherSchema["schema"] = json.RawMessage(`"codefly.runnable-prepared/v2"`)
	withOtherSchema, err := json.Marshal(otherSchema)
	require.NoError(t, err)
	_, err = runnable.DecodePrepared(withOtherSchema)
	require.ErrorIs(t, err, runnable.ErrInvalid)

	_, err = runnable.DecodePrepared(nil)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	_, err = runnable.DecodePrepared([]byte(strings.Repeat("x", runnable.MaxPreparedBytes+1)))
	require.ErrorIs(t, err, runnable.ErrInvalid)
	_, err = runnable.DecodePrepared([]byte("not a document"))
	require.ErrorIs(t, err, runnable.ErrInvalid)
}

// A worker installing the derived operations of its workspace receives every
// prepared binding as workspace configuration. The form that carried an
// owner's descriptor closure per operation put a 150 KB string into the
// environment for each, which Linux refuses at exec; these are small enough
// that none of them is delivered by file at all.
func TestAWorkerReceivingTwentyFourOperationsStaysUnderLinuxLimits(t *testing.T) {
	info := &basev0.ConfigurationInformation{Name: "runnable-bindings"}
	for i := 0; i < 24; i++ {
		binding := connectBinding()
		binding.Operation.Spelling = fmt.Sprintf("/acme.items.v1.Items/Apply%02d", i)
		binding.Call.Route = &runnablev0.PreparedCall_Connect{Connect: &runnablev0.ConnectProcedure{Procedure: binding.Operation.GetSpelling()}}
		info.ConfigurationValues = append(info.ConfigurationValues, &basev0.ConfigurationValue{
			Key:   fmt.Sprintf("ACME__OPERATION_%02d", i),
			Value: string(encoded(t, binding)),
		})
	}
	configuration := &basev0.Configuration{Origin: resources.ConfigurationWorkspace, Infos: []*basev0.ConfigurationInformation{info}}

	envs, err := resources.ConfigurationAsEnvironmentVariables(configuration, "local", false)
	require.NoError(t, err)
	require.Len(t, envs, 24)
	total := 0
	for _, env := range envs {
		require.False(t, env.File, env.Key)
	}
	require.NoError(t, resources.CheckProcessEnvironment(envs))
	for _, entry := range resources.EnvironmentVariableAsStrings(envs) {
		require.Less(t, len(entry)+1, resources.MaxEnvironmentStringBytes, strings.SplitN(entry, "=", 2)[0])
		total += len(entry) + 1
	}
	require.Less(t, total, resources.MaxEnvironmentStringBytes)
	t.Logf("24 operations: %d environment bytes, no file carrier", total)
}
