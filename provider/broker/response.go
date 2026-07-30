package broker

import (
	"fmt"
	"io"
	"net/http"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/codefly-dev/core/provider/responsepolicy"
)

// handleResponse reads a bounded response, filters it through the manifest
// response policy, and materializes the safe result. It fails closed on any
// filtering error so original bytes are never forwarded, and it arms the
// capture gate so a durable capture must be checkpointed before the next
// external request.
func (s *Session) handleResponse(descriptor manifest.RequestDescriptor, resp *http.Response) (*providerv0.ExecuteRequestResponse, error) {
	policy, err := s.responsePolicyFor(descriptor)
	if err != nil {
		return nil, err
	}
	// Read one byte past the budget: the policy rejects an over-budget body
	// rather than silently truncating a successful response.
	limit := s.limits.MaxCompressedBytes
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("response exceeds byte budget")
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

func certaintyForStatus(status int) providerv0.OutcomeCertainty {
	if status >= 200 && status < 300 {
		return providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE
	}
	return providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_PARTIAL
}
