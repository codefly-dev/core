package llm

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
)

// EmbedRequest is a typed embedding request. InputType is Voyage's retrieval
// hint ("query" or "document"); Dimensions requests a truncated vector — Voyage
// reads it as output_dimension, OpenAI as dimensions. Both are omitted when zero
// or empty so a vendor that does not accept the field never receives it.
type EmbedRequest struct {
	Model      string
	Input      []string
	InputType  string
	Dimensions int64
}

// body renders the request as descriptor-allowed body fields. PlannedEmbed
// screens content and validates the fields against the target descriptor; this
// method does neither.
//
// Dimensions is not rendered here: its field name is vendor-specific
// (output_dimension for Voyage, dimensions for OpenAI), so PlannedEmbed resolves
// it against the target descriptor's allowed fields.
func (r EmbedRequest) body() map[string]*providerv0.PublicValue {
	inputs := make([]*providerv0.PublicValue, 0, len(r.Input))
	for _, input := range r.Input {
		inputs = append(inputs, stringValue(input))
	}
	body := map[string]*providerv0.PublicValue{
		"model": stringValue(r.Model),
		"input": listValue(inputs),
	}
	if r.InputType != "" {
		body["input_type"] = stringValue(r.InputType)
	}
	return body
}

// content returns the free-form input strings for secret screening.
func (r EmbedRequest) content() []string {
	return r.Input
}

// Embedding is one embedding vector.
type Embedding []float64

// EmbeddingResponse is the decoded embedding result: one vector per input, in
// input order, plus token accounting.
type EmbeddingResponse struct {
	Embeddings []Embedding
	Usage      Usage
}

// DecodeEmbedding decodes the filtered forwarded fields into ordered embedding
// vectors.
func DecodeEmbedding(response *providerv0.ExecuteRequestResponse) (*EmbeddingResponse, error) {
	result := &EmbeddingResponse{}
	vectors := map[int]Embedding{}
	for _, field := range response.GetForwarded() {
		if index, ok := embeddingIndex(field.GetSelector()); ok {
			vector, err := toVector(field.GetValue())
			if err != nil {
				return nil, fmt.Errorf("embedding %d: %w", index, err)
			}
			vectors[index] = vector
			continue
		}
		switch field.GetSelector() {
		case "$.usage.input_tokens", "$.usage.prompt_tokens":
			result.Usage.InputTokens = field.GetValue().GetIntegerValue()
		case "$.usage.total_tokens":
			result.Usage.TotalTokens = field.GetValue().GetIntegerValue()
		}
	}
	indices := make([]int, 0, len(vectors))
	for index := range vectors {
		indices = append(indices, index)
	}
	sort.Ints(indices)
	for _, index := range indices {
		result.Embeddings = append(result.Embeddings, vectors[index])
	}
	return result, nil
}

// embeddingIndex reports the array index of a $.data[N].embedding selector.
func embeddingIndex(selector string) (int, bool) {
	rest, ok := strings.CutPrefix(selector, "$.data[")
	if !ok {
		return 0, false
	}
	digits, ok := strings.CutSuffix(rest, "].embedding")
	if !ok {
		return 0, false
	}
	index, err := strconv.Atoi(digits)
	if err != nil {
		return 0, false
	}
	return index, true
}

// toVector converts a forwarded list of numbers into an embedding vector.
func toVector(value *providerv0.PublicValue) (Embedding, error) {
	list := value.GetListValue()
	if list == nil {
		return nil, fmt.Errorf("embedding is not a list")
	}
	vector := make(Embedding, 0, len(list.GetValues()))
	for _, item := range list.GetValues() {
		component, err := toFloat(item)
		if err != nil {
			return nil, err
		}
		vector = append(vector, component)
	}
	return vector, nil
}

func toFloat(value *providerv0.PublicValue) (float64, error) {
	switch kind := value.GetKind().(type) {
	case *providerv0.PublicValue_DecimalValue:
		return strconv.ParseFloat(kind.DecimalValue, 64)
	case *providerv0.PublicValue_IntegerValue:
		return float64(kind.IntegerValue), nil
	default:
		return 0, fmt.Errorf("embedding component is not a number")
	}
}
