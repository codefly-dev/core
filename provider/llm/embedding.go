package llm

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
)

// EmbedRequest is a typed embedding request.
type EmbedRequest struct {
	Model string
	Input []string
}

// Body renders the request as descriptor-allowed body fields. It does no secret
// screening; callers screen r.content() with ScreenContent first.
func (r EmbedRequest) Body() map[string]*providerv0.PublicValue {
	inputs := make([]*providerv0.PublicValue, 0, len(r.Input))
	for _, input := range r.Input {
		inputs = append(inputs, stringValue(input))
	}
	return map[string]*providerv0.PublicValue{
		"model": stringValue(r.Model),
		"input": listValue(inputs),
	}
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
		if field.GetSelector() == "$.usage.input_tokens" {
			result.Usage.InputTokens = field.GetValue().GetIntegerValue()
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
