package broker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/provider/manifest"
	"github.com/codefly-dev/core/provider/responsepolicy"
)

// isEventStream reports whether a response body is a Server-Sent Events stream.
func isEventStream(contentType string) bool {
	media, _, _ := strings.Cut(contentType, ";")
	return strings.EqualFold(strings.TrimSpace(media), "text/event-stream")
}

// handleStream turns a received SSE body into an ordered sequence of filtered
// events. The response-policy filter runs over every event's data exactly as it
// runs over a whole non-streaming body, so a stream can never smuggle a
// secret-bearing or undeclared field past filtering. The whole stream is
// bounded by the response-bytes budget, and any drift — an unparseable frame or
// a filtering failure on any event — fails closed, forwarding nothing.
func (s *Session) handleStream(descriptor manifest.RequestDescriptor, resp *http.Response) (*providerv0.ExecuteRequestResponse, error) {
	policy, err := s.streamPolicyFor(descriptor)
	if err != nil {
		return nil, err
	}
	// Read one byte past the budget so an over-budget stream is rejected rather
	// than silently truncated: the byte budget bounds the entire stream, not one
	// frame.
	limit := s.limits.MaxCompressedBytes
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read response stream: %w", err)
	}
	if int64(len(raw)) > limit {
		return nil, fmt.Errorf("response stream exceeds byte budget")
	}
	frames, err := parseSSE(raw)
	if err != nil {
		return nil, err
	}
	response := &providerv0.ExecuteRequestResponse{
		StatusCode: uint32(resp.StatusCode),
		Certainty:  providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE,
	}
	ctx := resp.Request.Context()
	for _, frame := range frames {
		event := &providerv0.FilteredEvent{EventType: frame.event}
		if !frame.done {
			if err := s.filterFrame(ctx, policy, frame, event, response); err != nil {
				return nil, err
			}
		}
		response.Events = append(response.Events, event)
	}
	if n := len(response.Events); n > 0 {
		response.Events[n-1].Terminal = true
	}
	return response, nil
}

// filterFrame filters one event's JSON data through the policy and records the
// safe result on the event. Captures are also aggregated onto the response so
// the session's capture gate arms exactly as it does on the non-streaming path.
func (s *Session) filterFrame(
	ctx context.Context,
	policy responsepolicy.Policy,
	frame sseFrame,
	event *providerv0.FilteredEvent,
	response *providerv0.ExecuteRequestResponse,
) error {
	result, err := policy.Filter(ctx, frame.data, "", "application/json", s.sink)
	if err != nil {
		return fmt.Errorf("filter stream event: %w", err)
	}
	for _, forwarded := range result.Forwarded {
		event.Forwarded = append(event.Forwarded, &providerv0.FilteredField{
			Selector: forwarded.Selector,
			Value:    forwarded.Value,
		})
	}
	event.SuppressedPresence = append(event.SuppressedPresence, result.Suppressed...)
	for _, capture := range result.Captures {
		if capture.Outcome != responsepolicy.OutcomeCaptured {
			continue
		}
		captured := &providerv0.CaptureResult{
			CaptureId:     capture.Selector,
			Selector:      capture.Selector,
			SinkReference: capture.Reference,
			Captured:      true,
		}
		event.Captures = append(event.Captures, captured)
		response.Captures = append(response.Captures, captured)
	}
	return nil
}

// sseFrame is one parsed Server-Sent Events frame. done marks the OpenAI-style
// `data: [DONE]` sentinel, whose payload is never JSON and so is never filtered.
type sseFrame struct {
	event string
	data  []byte
	done  bool
}

// parseSSE parses an SSE body into ordered frames. It follows the dispatch rule
// of the WHATWG event-stream grammar: lines accumulate into an event that is
// dispatched on a blank line, multiple data lines join with a newline, and
// comment lines (starting with a colon) are ignored. A frame that accumulated
// neither an event name nor data is not dispatched.
func parseSSE(raw []byte) ([]sseFrame, error) {
	var (
		frames []sseFrame
		event  string
		data   []string
		seen   bool
	)
	dispatch := func() {
		if !seen {
			return
		}
		payload := []byte(strings.Join(data, "\n"))
		frames = append(frames, sseFrame{
			event: event,
			data:  payload,
			done:  string(payload) == "[DONE]",
		})
		event, data, seen = "", nil, false
	}
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) == 0 {
			dispatch()
			continue
		}
		if line[0] == ':' {
			continue
		}
		field, value, _ := strings.Cut(string(line), ":")
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			event, seen = value, true
		case "data":
			data, seen = append(data, value), true
		case "id", "retry":
			// Volatile framing fields never leave the host: they are neither
			// forwarded nor recorded, so a re-record stays byte-stable.
		default:
			return nil, fmt.Errorf("unrecognized event-stream field %q", field)
		}
	}
	dispatch()
	return frames, nil
}
