package runnable_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	runnablev0 "github.com/codefly-dev/core/generated/go/codefly/runnable/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/runnable"
)

// pdfTables is an authored tool: a PDF document in, rows out, offered to a
// caller as a read-only tool under the module's own kind.
func pdfTables() *resources.Runnable {
	return &resources.Runnable{
		Kind:    resources.RunnableKind,
		Name:    "pdf-extract-tables",
		Version: "0.1.0",
		Agent:   &resources.Agent{Kind: resources.RunnableAgent, Name: "python", Version: "0.0.7", Publisher: "codefly.dev"},
		Contract: &resources.RunnableContract{
			Protocol: resources.RunnableServedProtocolV1,
			Input: &resources.RunnableSchema{Fields: []*resources.RunnableField{
				{Name: "pdf", Type: resources.RunnableFieldFile, MediaTypes: []string{"application/pdf"}},
			}},
			Output: &resources.RunnableSchema{Fields: []*resources.RunnableField{
				{Name: "rows", Type: resources.RunnableFieldArray, Items: &resources.RunnableField{
					Type: resources.RunnableFieldArray, Items: &resources.RunnableField{Type: resources.RunnableFieldString}}},
			}},
		},
		Entrypoint: &resources.RunnableEntrypoint{Handler: "handler.py"},
		Execution: &resources.RunnableExecution{
			Facilities:   []resources.RunnableFacility{resources.RunnableFacilityGenerated},
			Timeout:      "30s",
			Cancellation: resources.RunnableCancellationNone,
			Recovery:     resources.RunnableRecoveryRecompute,
		},
		Operation: map[string]any{
			"audience":      "workbench",
			"invoke_scopes": []any{map[string]any{"resource_kind": "workbench.tools", "actions": []any{"use", "read"}}},
			"lookup_scopes": []any{map[string]any{"resource_kind": "workbench.tools", "actions": []any{"read"}}},
			"tool": map[string]any{
				"name":        "pdf_extract_tables",
				"description": "Extract the tables of a PDF document as rows of cell text.",
				"effect":      "EFFECT_READ_ONLY",
			},
		},
	}
}

func TestAuthoredOperationDerivesTheBoundsItsExecutionStates(t *testing.T) {
	spec, err := runnable.AuthoredOperation(pdfTables())
	require.NoError(t, err)
	require.Equal(t, runnable.ServedInvokeProcedure, spec.Method)
	require.Equal(t, 30*time.Second, spec.AttemptTimeout)
	require.Equal(t, 30*time.Second, spec.TotalTimeout)
	require.Equal(t, uint32(1), spec.MaxAttempts, "a harness call is not retried unless its author says so")
	require.Equal(t, basev0.RunnableExecution_COMPLETION_CALL, spec.Completion)
	require.Equal(t, resources.DefaultRunnablePayloadBytes, spec.MaxInputBytes)
	require.Empty(t, spec.LookupMethod, "a recompute runnable has no receipt route")
	require.Equal(t, "pdf_extract_tables", spec.Tool.GetName())
	require.Equal(t, runnablev0.ToolExposure_EFFECT_READ_ONLY, spec.Tool.GetEffect())

	receipt := pdfTables()
	receipt.Execution.Recovery = resources.RunnableRecoveryReceipt
	receipt.Operation["max_attempts"] = 3
	receipt.Operation["backoff"] = "2s"
	receipt.Operation["retryable_codes"] = []any{"UNAVAILABLE"}
	spec, err = runnable.AuthoredOperation(receipt)
	require.NoError(t, err)
	require.Equal(t, runnable.ServedLookupProcedure, spec.LookupMethod)
	require.Equal(t, 3*30*time.Second+2*2*time.Second, spec.TotalTimeout, "the total covers every attempt and the backoffs between them")
}

func TestAuthoredOperationRefusesWhatItCannotInstall(t *testing.T) {
	_, err := runnable.AuthoredOperation(func() *resources.Runnable { r := pdfTables(); r.Operation = nil; return r }())
	require.True(t, errors.Is(err, runnable.ErrNotAnOperation), "no operation block is an answer, not a failure: %v", err)

	cases := []struct {
		name   string
		mutate func(r *resources.Runnable)
		want   string
	}{
		{"a bound the execution block owns", func(r *resources.Runnable) { r.Operation["total_timeout"] = "10s" }, "total_timeout, which its execution block owns"},
		{"a field core does not define", func(r *resources.Runnable) { r.Operation["sandbox"] = "off" }, "not a codefly.runnable.v0.Operation document"},
		{"no authority", func(r *resources.Runnable) {
			delete(r.Operation, "invoke_scopes")
			delete(r.Operation, "lookup_scopes")
		}, "declares no invoke_scopes"},
		{"a tool with no effect", func(r *resources.Runnable) {
			r.Operation["tool"] = map[string]any{"name": "pdf_extract_tables", "description": "Extract tables."}
		}, "explicitly declare read-only or mutation"},
		{"an attempt longer than an installer admits", func(r *resources.Runnable) { r.Execution.Timeout = "2m" }, "attempt_timeout 2m0s is outside"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := pdfTables()
			tc.mutate(r)
			_, err := runnable.AuthoredOperation(r)
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// The prepared binding of a harness is the shape a derived operation's is,
// so a caller that installs one installs the other: the Connect procedure the
// harness answers, the contract with its digest, the policy with its tool.
func TestPrepareGeneratedServiceIsAPreparedBindingCallersAlreadyRead(t *testing.T) {
	r := pdfTables()
	spec, err := runnable.AuthoredOperation(r)
	require.NoError(t, err)
	binding, err := runnable.PrepareGeneratedService(r, "workbench", "http://localhost:31337", spec.Policy())
	require.NoError(t, err)
	require.Equal(t, runnable.ServedInvokeProcedure, binding.GetCall().GetConnect().GetProcedure())
	require.Equal(t, runnable.GeneratedServiceEndpoint, binding.GetOperation().GetEndpoint())
	require.Equal(t, "pdf-extract-tables", binding.GetOperation().GetService())

	encoded, err := runnable.EncodePrepared(binding)
	require.NoError(t, err)
	decoded, err := runnable.DecodePrepared(encoded)
	require.NoError(t, err)
	tool, err := runnable.ToolFromPrepared(decoded)
	require.NoError(t, err)
	require.Equal(t, "pdf_extract_tables", tool.GetName())
	require.Equal(t, []string{"application/pdf"}, decoded.GetContract().GetInput().GetFields()[0].GetMediaTypes())

	_, err = runnable.PrepareGeneratedService(r, "workbench", "localhost:31337", spec.Policy())
	require.ErrorContains(t, err, "address", "a bare host:port is not the HTTP base URL a JSON call is sent to")
}

func TestFileFieldsWalksNestedDocuments(t *testing.T) {
	schema := &basev0.RunnableSchema{Fields: []*basev0.RunnableField{
		{Name: "pdf", Type: basev0.RunnableField_FILE},
		{Name: "pages", Type: basev0.RunnableField_ARRAY, Items: &basev0.RunnableField{Type: basev0.RunnableField_OBJECT, Fields: []*basev0.RunnableField{
			{Name: "scan", Type: basev0.RunnableField_FILE}, {Name: "label", Type: basev0.RunnableField_STRING},
		}}},
	}}
	require.Equal(t, []string{"pdf", "pages[].scan"}, runnable.FileFields(schema))
}

func TestFileValueCarriesADocumentAndNeverAPath(t *testing.T) {
	field := &basev0.RunnableField{Name: "pdf", Type: basev0.RunnableField_FILE, MediaTypes: []string{"application/pdf"}}
	content := base64.StdEncoding.EncodeToString([]byte("%PDF-1.7"))
	value := func(members map[string]any) json.RawMessage {
		raw, err := json.Marshal(members)
		require.NoError(t, err)
		return raw
	}

	decoded, err := runnable.DecodeFileValue(field, runnable.FileInput, value(map[string]any{
		"reference": "store://tenant/entry/e1@v3", "media_type": "application/pdf", "name": "q3.pdf", "content": content,
	}), nil)
	require.NoError(t, err)
	require.Equal(t, []byte("%PDF-1.7"), decoded.Content)

	// An output may version the document it was handed, and nothing else.
	_, err = runnable.DecodeFileValue(field, runnable.FileOutput, value(map[string]any{
		"reference": "store://tenant/entry/e1@v3", "media_type": "application/pdf", "content": content,
	}), []string{"store://tenant/entry/e1@v3"})
	require.NoError(t, err)

	cases := []struct {
		name    string
		side    runnable.FileSide
		members map[string]any
		raw     string
		want    string
	}{
		{"an input with no reference", runnable.FileInput, map[string]any{"media_type": "application/pdf", "content": content}, "", "must carry a reference"},
		{"a reference with whitespace", runnable.FileInput, map[string]any{"reference": "a b", "media_type": "application/pdf", "content": content}, "", "must carry a reference"},
		{"a media type the field does not admit", runnable.FileInput, map[string]any{"reference": "r", "media_type": "text/html", "content": content}, "", "is not one of"},
		{"a name that is a path", runnable.FileInput, map[string]any{"reference": "r", "media_type": "application/pdf", "name": "../etc/passwd", "content": content}, "", "without a path separator"},
		{"a path member", runnable.FileInput, map[string]any{"reference": "r", "media_type": "application/pdf", "content": content, "path": "/tmp/x"}, "", `member "path" is not one of`},
		{"unpadded content", runnable.FileInput, map[string]any{"reference": "r", "media_type": "application/pdf", "content": "JVBERi0xLjc"}, "", "not standard padded base64"},
		{"no content", runnable.FileInput, map[string]any{"reference": "r", "media_type": "application/pdf"}, "", "carries no content"},
		{"an output versioning a document it was not handed", runnable.FileOutput, map[string]any{"reference": "other", "media_type": "application/pdf", "content": content}, "", "no input document of this call"},
		{"a repeated member", runnable.FileInput, nil, `{"reference":"r","reference":"s","media_type":"application/pdf","content":"` + content + `"}`, "repeated"},
		{"not an object", runnable.FileInput, nil, `"store://tenant/entry/e1"`, "must be an object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			raw := json.RawMessage(tc.raw)
			if tc.members != nil {
				raw = value(tc.members)
			}
			_, err := runnable.DecodeFileValue(field, tc.side, raw, []string{"r"})
			require.ErrorContains(t, err, tc.want)
		})
	}
}
