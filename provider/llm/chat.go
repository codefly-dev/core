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
// contributed, if any; Terminal marks the final event of the stream.
type ChatDelta struct {
	EventType string
	Text      string
	Terminal  bool
}

// ChatStream is the reconstructed typed stream of chat deltas plus the assembled
// text and terminal metadata.
type ChatStream struct {
	Deltas     []ChatDelta
	Text       string
	StopReason string
	Usage      Usage
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
		delta := ChatDelta{EventType: event.GetEventType(), Terminal: event.GetTerminal()}
		for _, field := range event.GetForwarded() {
			if field.GetSelector() == "$.delta.text" {
				delta.Text += field.GetValue().GetStringValue()
				continue
			}
			applyChatField(field, &stream.StopReason, &stream.Usage)
		}
		assembled.WriteString(delta.Text)
		stream.Deltas = append(stream.Deltas, delta)
	}
	stream.Text = assembled.String()
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

// ReconstructSSE re-emits the filtered events as a well-formed Server-Sent
// Events body. Each event becomes an `event:`/`data:` frame carrying the safe
// projection of its forwarded fields, in order, and the terminal event closes
// the stream. It reconstructs only the safe, filtered material — the broker
// never retains raw vendor bytes — so framing and ordering are reproduced
// without ever replaying an unfiltered field.
func ReconstructSSE(events []*providerv0.FilteredEvent) []byte {
	var builder strings.Builder
	for _, event := range events {
		if event.GetEventType() != "" {
			fmt.Fprintf(&builder, "event: %s\n", event.GetEventType())
		}
		builder.WriteString("data: ")
		builder.WriteString(safeProjection(event.GetForwarded()))
		builder.WriteString("\n\n")
	}
	return []byte(builder.String())
}

// safeProjection renders forwarded fields as a canonical selector-keyed JSON
// object — the same shape responsepolicy preserves, so the frame is byte-stable.
func safeProjection(fields []*providerv0.FilteredField) string {
	pairs := make([]string, 0, len(fields))
	for _, field := range fields {
		pairs = append(pairs, fmt.Sprintf("%q:%s", field.GetSelector(), scalarJSON(field.GetValue())))
	}
	sort.Strings(pairs)
	return "{" + strings.Join(pairs, ",") + "}"
}

func scalarJSON(value *providerv0.PublicValue) string {
	switch kind := value.GetKind().(type) {
	case *providerv0.PublicValue_StringValue:
		return fmt.Sprintf("%q", kind.StringValue)
	case *providerv0.PublicValue_IntegerValue:
		return fmt.Sprintf("%d", kind.IntegerValue)
	case *providerv0.PublicValue_DecimalValue:
		return kind.DecimalValue
	case *providerv0.PublicValue_BoolValue:
		return fmt.Sprintf("%t", kind.BoolValue)
	default:
		return "null"
	}
}
