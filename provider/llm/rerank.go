package llm

import (
	"fmt"
	"sort"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
)

// RerankRequest is a typed rerank request: score each document against the query
// and return them ordered by relevance. TopK, when set, bounds how many results
// the vendor returns.
type RerankRequest struct {
	Model     string
	Query     string
	Documents []string
	TopK      int64
}

// body renders the request as descriptor-allowed body fields. PlannedRerank
// screens content and validates the fields against the target descriptor; this
// method does neither.
func (r RerankRequest) body() map[string]*providerv0.PublicValue {
	documents := make([]*providerv0.PublicValue, 0, len(r.Documents))
	for _, document := range r.Documents {
		documents = append(documents, stringValue(document))
	}
	body := map[string]*providerv0.PublicValue{
		"model":     stringValue(r.Model),
		"query":     stringValue(r.Query),
		"documents": listValue(documents),
	}
	if r.TopK != 0 {
		body["top_k"] = integerValue(r.TopK)
	}
	return body
}

// content returns the free-form query and documents for secret screening.
func (r RerankRequest) content() []string {
	return append([]string{r.Query}, r.Documents...)
}

// RerankResult is one scored document, identified by its index in the request's
// documents and its relevance score.
type RerankResult struct {
	Index int
	Score float64
}

// RerankResponse is the decoded rerank result: the scored documents in the
// vendor's returned order, plus token accounting.
type RerankResponse struct {
	Results []RerankResult
	Usage   Usage
}

// DecodeRerank decodes the filtered forwarded fields into scored results ordered
// by the returned data array position.
func DecodeRerank(response *providerv0.ExecuteRequestResponse) (*RerankResponse, error) {
	result := &RerankResponse{}
	results := map[int]RerankResult{}
	for _, field := range response.GetForwarded() {
		if position, ok := indexBetween(field.GetSelector(), "$.data[", "].index"); ok {
			entry := results[position]
			entry.Index = int(field.GetValue().GetIntegerValue())
			results[position] = entry
			continue
		}
		if position, ok := indexBetween(field.GetSelector(), "$.data[", "].relevance_score"); ok {
			score, err := toFloat(field.GetValue())
			if err != nil {
				return nil, fmt.Errorf("rerank result %d: %w", position, err)
			}
			entry := results[position]
			entry.Score = score
			results[position] = entry
			continue
		}
		if field.GetSelector() == "$.usage.total_tokens" {
			result.Usage.TotalTokens = field.GetValue().GetIntegerValue()
		}
	}
	positions := make([]int, 0, len(results))
	for position := range results {
		positions = append(positions, position)
	}
	sort.Ints(positions)
	for _, position := range positions {
		result.Results = append(result.Results, results[position])
	}
	return result, nil
}
