package broker

import (
	"fmt"
	"io"
	"net/http"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/codefly-dev/core/provider/responsepolicy"
)

// handleResponse turns a received response into a filtered safe response. The
// manifest response schema describes successful bodies only, so a non-success
// response is surfaced by status alone with its untrusted body dropped, and a
// response that carries no body by HTTP semantics is surfaced by status alone.
// A successful body is filtered through the schema and fails closed on any
// filtering error — but the delivery status is always preserved, never lost to
// a bare error.
func (s *Session) handleResponse(descriptor manifest.RequestDescriptor, resp *http.Response) (*providerv0.ExecuteRequestResponse, error) {
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// A non-success body is untrusted and carries no schema-declared fields;
		// drop it entirely so an attacker-controlled, secret-looking error body
		// never reaches a provider, log, or persisted surface.
		return &providerv0.ExecuteRequestResponse{
			StatusCode: uint32(resp.StatusCode),
			Certainty:  certaintyForStatus(resp.StatusCode),
		}, nil
	}
	policy, err := s.responsePolicyFor(descriptor)
	if err != nil {
		return nil, err
	}
	// Read one byte past the budget: the policy rejects an over-budget body
	// rather than silently truncating a successful response. HEAD and no-content
	// responses read empty.
	limit := s.limits.MaxCompressedBytes
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("response exceeds byte budget")
	}
	if len(raw) == 0 {
		// A body-carrying success is expected whenever the schema requires a
		// field; an empty body then is drift and fails closed. Otherwise a
		// bodyless success (HEAD, 204, 205) is surfaced by status alone.
		if policy.RequiresBody() {
			return nil, fmt.Errorf("successful response has no body but the schema requires fields")
		}
		return &providerv0.ExecuteRequestResponse{
			StatusCode: uint32(resp.StatusCode),
			Certainty:  providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE,
		}, nil
	}
	result, err := policy.Filter(resp.Request.Context(), raw, resp.Header.Get("Content-Encoding"), resp.Header.Get("Content-Type"), s.sink)
	if err != nil {
		return nil, fmt.Errorf("filter response: %w", err)
	}

	response := &providerv0.ExecuteRequestResponse{
		StatusCode:         uint32(resp.StatusCode),
		Certainty:          certaintyForStatus(resp.StatusCode),
		SuppressedPresence: result.Suppressed,
	}
	for _, forwarded := range result.Forwarded {
		response.Forwarded = append(response.Forwarded, &providerv0.FilteredField{
			Selector: forwarded.Selector,
			Value:    forwarded.Value,
		})
	}
	for _, capture := range result.Captures {
		if capture.Outcome != responsepolicy.OutcomeCaptured {
			continue
		}
		response.Captures = append(response.Captures, &providerv0.CaptureResult{
			CaptureId:     capture.Selector,
			Selector:      capture.Selector,
			SinkReference: capture.Reference,
			Captured:      true,
		})
	}
	return response, nil
}

// certaintyForStatus maps a received status to the host's certainty about the
// remote effect. A 2xx is complete; a 4xx is a definitive client-side rejection,
// so the remote state is likewise known; a 3xx (surfaced because redirects are
// never followed) or 5xx leaves the remote effect genuinely unknown.
func certaintyForStatus(status int) providerv0.OutcomeCertainty {
	switch {
	case status >= 200 && status < 300:
		return providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE
	case status >= 400 && status < 500:
		return providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE
	default:
		return providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_UNCERTAIN
	}
}
