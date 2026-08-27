package broker

import (
	"bufio"
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
// events. It reads frame by frame rather than buffering the whole body, so each
// event is filtered and surfaced to the caller (via OnStreamEvent) the moment it
// arrives — that is what makes the streaming path live rather than a deferred
// dump. The response-policy filter runs over every event's data exactly as it
// runs over a whole non-streaming body, so a stream can never smuggle a
// secret-bearing or undeclared field past filtering. The running byte total is
// bounded by the response-bytes budget, and any drift — an unfilterable event,
// an over-budget stream — fails closed, forwarding nothing further.
func (s *Session) handleStream(descriptor manifest.RequestDescriptor, resp *http.Response) (*providerv0.ExecuteRequestResponse, error) {
	policy, err := s.streamPolicyFor(descriptor)
	if err != nil {
		return nil, err
	}
	ctx := resp.Request.Context()
	// Read one byte past the budget so an over-budget stream is rejected rather
	// than silently truncated: the byte budget bounds the entire stream.
	limit := s.limits.MaxCompressedBytes
	reader := bufio.NewReader(io.LimitReader(resp.Body, limit+1))

	response := &providerv0.ExecuteRequestResponse{
		StatusCode: uint32(resp.StatusCode),
		Certainty:  providerv0.OutcomeCertainty_OUTCOME_CERTAINTY_COMPLETE,
	}
	var (
		builder sseBuilder
		read    int64
	)
	dispatch := func() error {
		frame, ok := builder.take()
		if !ok {
			return nil
		}
		event := &providerv0.FilteredEvent{EventType: frame.event}
		if !frame.done {
			if err := s.filterFrame(ctx, policy, frame, event); err != nil {
				return err
			}
		}
		response.Events = append(response.Events, event)
		return s.emitStreamEvent(event)
	}
	for {
		line, readErr := reader.ReadString('\n')
		read += int64(len(line))
		if read > limit {
			return nil, fmt.Errorf("response stream exceeds byte budget")
		}
		content := strings.TrimRight(line, "\r\n")
		blank := content == "" && strings.HasSuffix(line, "\n")
		switch {
		case blank:
			if err := dispatch(); err != nil {
				return nil, err
			}
		case content != "":
			builder.line(content)
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("read response stream: %w", readErr)
		}
	}
	// A final frame not terminated by a blank line is still dispatched.
	if err := dispatch(); err != nil {
		return nil, err
	}
	if n := len(response.Events); n > 0 {
		response.Events[n-1].Terminal = true
	}
	return response, nil
}

// filterFrame filters one event's JSON data through the policy and records the
// safe result on the event. Captures live only on the event; the session's
// capture gate inspects both event and top-level captures, so there is no need
// to duplicate them onto the response.
func (s *Session) filterFrame(
	ctx context.Context,
	policy responsepolicy.Policy,
	frame sseFrame,
	event *providerv0.FilteredEvent,
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
		event.Captures = append(event.Captures, &providerv0.CaptureResult{
			CaptureId:     capture.Selector,
			Selector:      capture.Selector,
			SinkReference: capture.Reference,
			Captured:      true,
		})
	}
	return nil
}

// emitStreamEvent surfaces one filtered event to the caller's live callback.
func (s *Session) emitStreamEvent(event *providerv0.FilteredEvent) error {
	if s.onStreamEvent == nil {
		return nil
	}
	return s.onStreamEvent(event)
}

// emitStreamEvents surfaces an ordered set of already-filtered events to the
// live callback, used on the replay path where events arrive as a block.
func (s *Session) emitStreamEvents(events []*providerv0.FilteredEvent) error {
	for _, event := range events {
		if err := s.emitStreamEvent(event); err != nil {
			return err
		}
	}
	return nil
}

// responseHasCaptures reports whether a response captured any secret, on either
// the non-streaming top-level fields or a streamed event.
func responseHasCaptures(response *providerv0.ExecuteRequestResponse) bool {
	if len(response.GetCaptures()) > 0 {
		return true
	}
	for _, event := range response.GetEvents() {
		if len(event.GetCaptures()) > 0 {
			return true
		}
	}
	return false
}

// sseFrame is one parsed Server-Sent Events frame. done marks the OpenAI-style
// `data: [DONE]` sentinel, whose payload is never JSON and so is never filtered.
type sseFrame struct {
	event string
	data  []byte
	done  bool
}

// sseBuilder accumulates the lines of one event-stream frame. It follows the
// WHATWG event-stream grammar: an `event`/`data` line sets the frame, multiple
// data lines join with a newline, a comment line (leading colon) is ignored,
// and — per the spec — any other field name (id, retry, or a vendor extension)
// is ignored rather than rejected, so a benign framing addition never fails a
// stream.
type sseBuilder struct {
	event string
	data  []string
	seen  bool
}

func (b *sseBuilder) line(content string) {
	if content[0] == ':' {
		return
	}
	field, value, _ := strings.Cut(content, ":")
	value = strings.TrimPrefix(value, " ")
	switch field {
	case "event":
		b.event, b.seen = value, true
	case "data":
		b.data, b.seen = append(b.data, value), true
	}
}

// take returns the accumulated frame and resets the builder. A frame that
// gathered neither an event name nor data is not dispatched.
func (b *sseBuilder) take() (sseFrame, bool) {
	if !b.seen {
		return sseFrame{}, false
	}
	payload := []byte(strings.Join(b.data, "\n"))
	frame := sseFrame{event: b.event, data: payload, done: string(payload) == "[DONE]"}
	b.event, b.data, b.seen = "", nil, false
	return frame, true
}
