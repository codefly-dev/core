package manifest

import (
	"encoding/json"
	"fmt"
	"sort"
)

// APIConsumesEnvironmentVariable carries a solution's api.consumes projection to
// its running backend. The value is the JSON encoding of []ConsumedAPI produced
// by Manifest.ConsumedAPIsEnvValue. It is absent for solutions that consume no
// APIs, so no backend behavior changes unless api.consumes is declared.
const APIConsumesEnvironmentVariable = "CODEFLY__API_CONSUMES"

// ConsumedAPI is the runtime-facing projection of one api.consumes entry: the
// identity a running backend needs to map a consumed prefix to its upstream
// endpoint. Module, Service, Endpoint, and Protocol name the producing endpoint
// by composition identity; a reader must feed them through
// resources.EndpointAsEnvironmentVariableKeyBase to reconstruct the injected
// CODEFLY__ENDPOINT__<MODULE>__<SERVICE>__<ENDPOINT>__<API> address key rather
// than hand-rolling the upper-case/hyphen transform. As is the facade
// entry-point (empty means the runtime applies its default), from which the
// runtime derives the /v1/<as> route.
type ConsumedAPI struct {
	ID       string `json:"id"`
	Module   string `json:"module"`
	Service  string `json:"service"`
	Endpoint string `json:"endpoint"`
	Protocol string `json:"protocol"`
	As       string `json:"as,omitempty"`
}

// ConsumedAPIs returns the runtime projection of the solution's api.consumes, in
// canonical (id-sorted) order. Nil when the solution consumes no bindable APIs.
func (m *Manifest) ConsumedAPIs() []ConsumedAPI {
	out := make([]ConsumedAPI, 0, len(m.API.Consumes))
	for _, declaration := range m.API.Consumes {
		// Unbound consumes (module/service/endpoint all empty, permitted by
		// validateConsumedAPIs) name no producing endpoint, so they carry no
		// upstream to federate — projecting them would force every reader to
		// rebuild a garbage CODEFLY__ENDPOINT key from empty segments.
		// Validation guarantees the binding is all-set or all-empty, so testing
		// Module alone is sufficient.
		if declaration.Module == "" {
			continue
		}
		out = append(out, ConsumedAPI{
			ID:       declaration.ID,
			Module:   declaration.Module,
			Service:  declaration.Service,
			Endpoint: declaration.Endpoint,
			Protocol: declaration.Protocol,
			As:       declaration.As,
		})
	}
	if len(out) == 0 {
		return nil
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ConsumedAPIsEnvValue encodes the consumed APIs as the CODEFLY__API_CONSUMES
// value. It returns the empty string when the solution consumes no bindable
// APIs, so a caller injects the variable only for solutions that declare a
// bound api.consumes. Marshaling a slice of plain-string structs cannot fail.
func (m *Manifest) ConsumedAPIsEnvValue() string {
	consumed := m.ConsumedAPIs()
	if len(consumed) == 0 {
		return ""
	}
	encoded, _ := json.Marshal(consumed)
	return string(encoded)
}

// ParseConsumedAPIs decodes a CODEFLY__API_CONSUMES value into the consumed
// targets. An empty value (unset variable) yields nil with no error, so a
// runtime reads it unconditionally.
func ParseConsumedAPIs(value string) ([]ConsumedAPI, error) {
	if value == "" {
		return nil, nil
	}
	var consumed []ConsumedAPI
	if err := json.Unmarshal([]byte(value), &consumed); err != nil {
		return nil, fmt.Errorf("decode %s: %w", APIConsumesEnvironmentVariable, err)
	}
	return consumed, nil
}
