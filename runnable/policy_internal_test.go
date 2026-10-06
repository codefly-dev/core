package runnable

import (
	"reflect"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/resources"
)

// fullSpec sets every field of OperationSpec to a valid non-zero value, so a
// field the conversion drops shows up as a difference and not as two matching
// zeros. It is the REST form because GRPCStatusNames is the zero vocabulary,
// and a zero there would be indistinguishable from a vocabulary never set.
func fullSpec() *OperationSpec {
	return &OperationSpec{
		Method:         "POST /v1/items",
		AttemptTimeout: 10 * time.Second,
		TotalTimeout:   time.Minute,
		MaxAttempts:    3,
		Backoff:        time.Second,
		RetryableCodes: []string{"503"},
		Codes:          HTTPStatusCodes,
		Audience:       "acme.items",
		InvokeScopes:   []*basev0.WorkScopeV1{{ResourceKind: "acme.item", Actions: []string{"read", "write"}}},
		LookupScopes:   []*basev0.WorkScopeV1{{ResourceKind: "acme.item", Actions: []string{"read"}}},
		LookupMethod:   "/acme.items.v1.Items/Lookup",
		MaxInputBytes:  1024,
		MaxOutputBytes: 2048,
		Completion:     basev0.RunnableExecution_COMPLETION_CALL,
		Tool:           &runnablev0.ToolExposure{Name: "apply_item", Description: "Apply an item change.", Effect: runnablev0.ToolExposure_EFFECT_MUTATION},
	}
}

// The policy a spec writes is the policy a binding reads back, field for
// field, with the schema as the list both are held to. This is what makes a
// hand-copied writer a reviewable defect instead of a silent one: a field the
// schema gains without a line in Policy is unset in every policy Policy writes,
// and a field preparedPolicy stops reading comes back different.
func TestPolicyRoundTripsEveryField(t *testing.T) {
	spec := fullSpec()
	require.NoError(t, spec.Validate())

	// The fixture may leave nothing at its zero value, or a dropped field would
	// compare equal to one that was never set.
	value := reflect.ValueOf(spec).Elem()
	for i := 0; i < value.NumField(); i++ {
		require.Falsef(t, value.Field(i).IsZero(),
			"fullSpec leaves OperationSpec.%s zero; set it so the round trip can see it", value.Type().Field(i).Name)
	}

	policy := spec.Policy()
	message := policy.ProtoReflect()
	fields := message.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		field := fields.Get(i)
		require.Truef(t, message.Has(field),
			"OperationSpec.Policy does not write Operation.%s; carry it on OperationSpec, write it in Policy and read it back in preparedPolicy", field.Name())
	}
	// Method and Codes are the binding's own — its operation spelling and the
	// vocabulary its route implies. Every other spec field is one wire field.
	require.Equal(t, fields.Len(), value.NumField()-2,
		"OperationSpec and Operation disagree on the policy's fields: a spec field that is not Method or Codes is exactly one wire field, and the reverse")

	binding := &runnablev0.PreparedBinding{
		Operation: &runnablev0.PreparedOperation{Module: "owner", Service: "items", Endpoint: "rest", Spelling: spec.Method},
		Call: &runnablev0.PreparedCall{
			Address: "http://items-rest.owner:8080",
			Route:   &runnablev0.PreparedCall_Rest{Rest: &runnablev0.HTTPRoute{Verb: "POST", Path: "/v1/items"}},
		},
		Contract: &basev0.RunnableContract{
			Protocol: resources.RunnableServiceProtocolV1,
			Input:    &basev0.RunnableSchema{Fields: []*basev0.RunnableField{{Name: "item", Type: basev0.RunnableField_STRING}}},
			Output:   &basev0.RunnableSchema{Fields: []*basev0.RunnableField{{Name: "applied", Type: basev0.RunnableField_BOOLEAN}}},
		},
		Policy: policy,
	}
	// EncodePrepared validates the delivered policy; a conversion that wrote
	// something Validate refuses would be refused here, not repaired.
	encoded, err := EncodePrepared(binding)
	require.NoError(t, err)
	delivered, err := DecodePrepared(encoded)
	require.NoError(t, err)
	read := preparedPolicy(delivered, HTTPStatusCodes)
	require.Equal(t, spec.Method, read.Method)
	require.Equal(t, spec.Codes, read.Codes)
	require.True(t, proto.Equal(policy, read.Policy()),
		"preparedPolicy does not read back what Policy wrote:\nwritten %v\nread    %v", policy, read.Policy())

	// The conversion is detached: a writer editing the policy it was handed
	// does not reach back into the spec it came from.
	policy.Tool.Name = "changed"
	policy.InvokeScopes[0].Actions[0] = "changed"
	policy.RetryableCodes[0] = "changed"
	require.Equal(t, "apply_item", spec.Tool.Name)
	require.Equal(t, "read", spec.InvokeScopes[0].Actions[0])
	require.Equal(t, "503", spec.RetryableCodes[0])
}
