package llm

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
)

// Message is one chat turn.
type Message struct {
	Role    string
	Content string
}

// ChatRequest is a typed chat completion request. It shapes the descriptor's
// allowed body fields; the host binds it into a PlannedRequest and runs it
// through the broker.
type ChatRequest struct {
	Model       string
	Messages    []Message
	MaxTokens   int64
	System      string
	Temperature string // canonical decimal string; omitted when empty
	Stream      bool
}

// Body renders the request as descriptor-allowed body fields.
//
// Prompt content is user text that may legitimately look secret-shaped; the
// provider protocol forbids secret-shaped structured values by design (see
// canonical validation), so callers must screen content with ScreenContent
// before building a request — Body itself does no screening.
func (r ChatRequest) Body() map[string]*providerv0.PublicValue {
	messages := make([]*providerv0.PublicValue, 0, len(r.Messages))
	for _, message := range r.Messages {
		messages = append(messages, objectValue(map[string]*providerv0.PublicValue{
			"role":    stringValue(message.Role),
			"content": stringValue(message.Content),
		}))
	}
	body := map[string]*providerv0.PublicValue{
		"model":      stringValue(r.Model),
		"messages":   listValue(messages),
		"max_tokens": integerValue(r.MaxTokens),
		"stream":     boolValue(r.Stream),
	}
	if r.System != "" {
		body["system"] = stringValue(r.System)
	}
	if r.Temperature != "" {
		body["temperature"] = decimalValue(r.Temperature)
	}
	return body
}

// content returns every free-form text field a caller supplies, so it can be
// screened against the provider secret heuristic before a request is built.
func (r ChatRequest) content() []string {
	texts := make([]string, 0, len(r.Messages)+1)
	for _, message := range r.Messages {
		texts = append(texts, message.Content)
	}
	if r.System != "" {
		texts = append(texts, r.System)
	}
	return texts
}

// Usage is the token accounting a chat call reports.
type Usage struct {
	InputTokens  int64
	OutputTokens int64
}

// ChatResponse is a whole non-streaming chat completion.
type ChatResponse struct {
	Text       string
	StopReason string
	Usage      Usage
}

// ChatDelta is one incremental streamed event. Text is the fragment this event
// contributed, if any; StopReason is set on the event that reports it; Terminal
// marks the final event of the delivered stream.
type ChatDelta struct {
	EventType  string
	Text       string
	StopReason string
	Usage      Usage
	Terminal   bool
}

// DecodeEvent types a single filtered stream event, for a caller wiring the
// broker's live OnStreamEvent callback to typed deltas as they arrive.
func DecodeEvent(event *providerv0.FilteredEvent) ChatDelta {
	delta := ChatDelta{EventType: event.GetEventType(), Terminal: event.GetTerminal()}
	for _, field := range event.GetForwarded() {
		if field.GetSelector() == "$.delta.text" {
			delta.Text += field.GetValue().GetStringValue()
			continue
		}
		applyChatField(field, &delta.StopReason, &delta.Usage)
	}
	return delta
}

// ChatStream is the reconstructed typed stream of chat deltas plus the assembled
// text and terminal metadata. Complete distinguishes a stream the model finished
// (a stop reason was reported) from one that ended early — a truncated stream
// closed cleanly mid-generation decodes with Complete false, so a consumer never
// mistakes truncation for a finished answer.
type ChatStream struct {
	Deltas     []ChatDelta
	Text       string
	StopReason string
	Usage      Usage
	Complete   bool
}

// DecodeChat decodes a whole non-streaming chat response from the filtered
// forwarded fields.
func DecodeChat(response *providerv0.ExecuteRequestResponse) (*ChatResponse, error) {
	if len(response.GetEvents()) > 0 {
		return nil, fmt.Errorf("response is a stream; use DecodeChatStream")
	}
	result := &ChatResponse{}
	texts := map[int]string{}
	for _, field := range response.GetForwarded() {
		if index, ok := contentIndex(field.GetSelector()); ok {
			texts[index] = field.GetValue().GetStringValue()
			continue
		}
		applyChatField(field, &result.StopReason, &result.Usage)
	}
	result.Text = joinIndexed(texts)
	return result, nil
}

// DecodeChatStream reconstructs the ordered typed stream from the filtered
// events. The assembled Text concatenates every text delta in event order.
func DecodeChatStream(response *providerv0.ExecuteRequestResponse) (*ChatStream, error) {
	if len(response.GetEvents()) == 0 {
		return nil, fmt.Errorf("response is not a stream; use DecodeChat")
	}
	stream := &ChatStream{}
	var assembled strings.Builder
	for _, event := range response.GetEvents() {
		delta := DecodeEvent(event)
		if delta.StopReason != "" {
			stream.StopReason = delta.StopReason
		}
		if delta.Usage.InputTokens != 0 {
			stream.Usage.InputTokens = delta.Usage.InputTokens
		}
		if delta.Usage.OutputTokens != 0 {
			stream.Usage.OutputTokens = delta.Usage.OutputTokens
		}
		assembled.WriteString(delta.Text)
		stream.Deltas = append(stream.Deltas, delta)
	}
	stream.Text = assembled.String()
	// A model that finished reports a stop reason; its absence means the stream
	// ended early (a truncated-but-clean close), which must not read as complete.
	stream.Complete = stream.StopReason != ""
	return stream, nil
}

// applyChatField maps one non-text forwarded field onto the decoded stop reason
// and usage.
func applyChatField(field *providerv0.FilteredField, stopReason *string, usage *Usage) {
	switch field.GetSelector() {
	case "$.stop_reason", "$.delta.stop_reason":
		*stopReason = field.GetValue().GetStringValue()
	case "$.usage.input_tokens", "$.message.usage.input_tokens":
		usage.InputTokens = field.GetValue().GetIntegerValue()
	case "$.usage.output_tokens", "$.message.usage.output_tokens":
		usage.OutputTokens = field.GetValue().GetIntegerValue()
	}
}

// contentIndex reports the array index of a $.content[N].text selector.
func contentIndex(selector string) (int, bool) {
	rest, ok := strings.CutPrefix(selector, "$.content[")
	if !ok {
		return 0, false
	}
	digits, ok := strings.CutSuffix(rest, "].text")
	if !ok {
		return 0, false
	}
	index, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}
	return index, true
}

// joinIndexed concatenates indexed text fragments in ascending index order.
func joinIndexed(texts map[int]string) string {
	indices := make([]int, 0, len(texts))
	for index := range texts {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	var builder strings.Builder
	for _, index := range indices {
		builder.WriteString(texts[index])
	}
	return builder.String()
}
