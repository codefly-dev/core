package runnable_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/runnable"
)

const applyTextRoute = "POST /ingest/text"

// declaredMarker is the policy the REST twin of the ingestion operation
// declares, written as the vendor extension the operation object would carry.
func declaredMarker() map[string]any {
	return map[string]any{
		"attempt_timeout": "10s",
		"total_timeout":   "60s",
		"max_attempts":    1,
		"backoff":         "1s",
		"retryable_codes": []any{"503"},
		"audience":        "documents.ingestion",
		"invoke_scopes":   []any{map[string]any{"resource_kind": "documents", "actions": []any{"ingest", "read"}}},
		"lookup_scopes":   []any{map[string]any{"resource_kind": "documents", "actions": []any{"read"}}},
	}
}

func markerSpec(t *testing.T, edit func(map[string]any)) (*runnable.OperationSpec, error) {
	t.Helper()
	declared := declaredMarker()
	if edit != nil {
		edit(declared)
	}
	marker, err := json.Marshal(declared)
	require.NoError(t, err)
	return runnable.OperationFromOpenAPIMarker(marker, applyTextRoute)
}

func TestOperationFromOpenAPIMarkerReadsTheDeclaredPolicy(t *testing.T) {
	spec, err := markerSpec(t, nil)
	require.NoError(t, err)
	require.Equal(t, applyTextRoute, spec.Method)
	require.Equal(t, 10*time.Second, spec.AttemptTimeout)
	require.Equal(t, time.Minute, spec.TotalTimeout)
	require.EqualValues(t, 1, spec.MaxAttempts)
	require.Equal(t, time.Second, spec.Backoff)
	require.Equal(t, []string{"503"}, spec.RetryableCodes)
	require.Equal(t, runnable.HTTPStatusCodes, spec.Codes)
	require.Equal(t, "documents.ingestion", spec.Audience)
	require.Empty(t, spec.LookupMethod)
	require.True(t, proto.Equal(&basev0.WorkScopeV1{ResourceKind: "documents", Actions: []string{"ingest", "read"}}, spec.InvokeScopes[0]))
	require.True(t, proto.Equal(&basev0.WorkScopeV1{ResourceKind: "documents", Actions: []string{"read"}}, spec.LookupScopes[0]))
}

// TestOperationFromOpenAPIMarkerReadsEitherFieldSpelling is why the marker is
// the proto3 JSON form of the option rather than a second schema: both
// spellings of a field name are one message, decoded once.
func TestOperationFromOpenAPIMarkerReadsEitherFieldSpelling(t *testing.T) {
	spec, err := markerSpec(t, func(declared map[string]any) {
		declared["attemptTimeout"] = declared["attempt_timeout"]
		delete(declared, "attempt_timeout")
		declared["maxAttempts"] = declared["max_attempts"]
		delete(declared, "max_attempts")
		declared["retryableCodes"] = declared["retryable_codes"]
		delete(declared, "retryable_codes")
	})
	require.NoError(t, err)
	require.Equal(t, 10*time.Second, spec.AttemptTimeout)
	require.Equal(t, uint32(1), spec.MaxAttempts)
	require.Equal(t, []string{"503"}, spec.RetryableCodes)
}

func TestOperationFromOpenAPIMarkerAnswersAnUnmarkedOperation(t *testing.T) {
	for _, absent := range []json.RawMessage{nil, {}} {
		_, err := runnable.OperationFromOpenAPIMarker(absent, applyTextRoute)
		require.ErrorIs(t, err, runnable.ErrNotAnOperation)
		require.ErrorContains(t, err, applyTextRoute)
	}
}

func TestOperationFromOpenAPIMarkerNamesRetryableOutcomesByHTTPStatus(t *testing.T) {
	spec, err := markerSpec(t, func(declared map[string]any) {
		// 520 is not in the IANA registry; a proxy in front of the owner still
		// answers with it, and a policy that retries on it is a real one.
		declared["retryable_codes"] = []any{"429", "502", "503", "504", "520"}
	})
	require.NoError(t, err)
	require.Equal(t, []string{"429", "502", "503", "504", "520"}, spec.RetryableCodes)

	for _, code := range []string{"UNAVAILABLE", "unavailable", "5xx", "0503", " 503", "+503", "", "99", "600", "1000", "503.0"} {
		t.Run(fmt.Sprintf("%q", code), func(t *testing.T) {
			_, err = markerSpec(t, func(declared map[string]any) {
				declared["retryable_codes"] = []any{code}
			})
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, "HTTP status")
		})
	}
}

func TestOperationFromOpenAPIMarkerRejectsAPolicyTheRuntimeWouldRefuse(t *testing.T) {
	for _, test := range []struct {
		name    string
		edit    func(map[string]any)
		because string
	}{
		{"unreadable", func(d map[string]any) { d["attempt_timeout"] = "ten seconds" }, "is not a codefly.runnable.v0.Operation"},
		{"unknown field", func(d map[string]any) { d["attempt_timeouts"] = "10s" }, "is not a codefly.runnable.v0.Operation"},
		{"short attempt", func(d map[string]any) { d["attempt_timeout"] = "0.5s" }, "attempt_timeout"},
		{"long attempt", func(d map[string]any) { d["attempt_timeout"] = "90s" }, "attempt_timeout"},
		{"total below one attempt", func(d map[string]any) { d["total_timeout"] = "5s" }, "total_timeout"},
		{"no attempts", func(d map[string]any) { delete(d, "max_attempts") }, "max_attempts"},
		{"too many attempts", func(d map[string]any) { d["max_attempts"] = 6 }, "max_attempts"},
		{"short backoff", func(d map[string]any) { d["backoff"] = "0.01s" }, "backoff"},
		{"repeated retryable code", func(d map[string]any) { d["retryable_codes"] = []any{"503", "503"} }, "twice"},
		{"no audience", func(d map[string]any) { delete(d, "audience") }, "audience"},
		{"no invoke scopes", func(d map[string]any) { delete(d, "invoke_scopes") }, "invoke_scopes"},
		{"no lookup scopes", func(d map[string]any) { delete(d, "lookup_scopes") }, "lookup_scopes"},
		{"widening lookup scope", func(d map[string]any) {
			d["lookup_scopes"] = []any{map[string]any{"resource_kind": "receipts", "actions": []any{"read"}}}
		}, "not covered by its invoke scopes"},
		{"writing lookup scope", func(d map[string]any) {
			d["lookup_scopes"] = []any{map[string]any{"resource_kind": "documents", "actions": []any{"ingest"}}}
		}, "not read-only"},
		// The marker marks the operation; its fields say how it runs, and an
		// attempt budget nobody chose is not one core invents on their behalf.
		{"empty", func(d map[string]any) {
			for key := range d {
				delete(d, key)
			}
		}, "declares no execution policy"},
		// A receipt lookup paired on the same service is a gRPC spelling, and
		// accepting one here would install a route nothing checks.
		{"lookup method", func(d map[string]any) { d["lookup_method"] = "GET /ingest/text/receipts" }, "lookup_method"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := markerSpec(t, test.edit)
			require.ErrorIs(t, err, runnable.ErrInvalid)
			require.ErrorContains(t, err, test.because)
			require.ErrorContains(t, err, applyTextRoute)
		})
	}
}

// TestOperationFromMethodStillNamesGRPCCodes guards the half the vocabulary
// split leaves alone: a method option names its retryable outcomes by
// google.rpc.Code and an HTTP status is not one of them.
func TestOperationFromMethodStillNamesGRPCCodes(t *testing.T) {
	declared := declaredOperation()
	declared.RetryableCodes = []string{"503"}
	_, _, err := runnable.PackageFromMethod(ingestionFiles(t, declared), ingestLocation(), ingestOwner(), applyText)
	require.ErrorIs(t, err, runnable.ErrInvalid)
	require.ErrorContains(t, err, "gRPC status code name")

	spec, err := markerSpec(t, nil)
	require.NoError(t, err)
	spec.Codes = runnable.GRPCStatusNames
	require.ErrorContains(t, spec.Validate(), "gRPC status code name")
}

func TestMarkerKeyIsTheOneTheDocumentSpells(t *testing.T) {
	require.Equal(t, "x-codefly-operation", runnable.OpenAPIOperationMarker)
	require.True(t, strings.HasPrefix(runnable.OpenAPIOperationMarker, "x-"), "an OpenAPI vendor extension starts with x-")
}
