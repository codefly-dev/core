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
// endpoint. Module, Service, and Endpoint name the producing endpoint by
// composition identity — the same names that form the injected
// CODEFLY__ENDPOINT__<MODULE>__<SERVICE>__<ENDPOINT>__<API> address key — so the
// runtime derives the upstream endpoint key without resolving the composition
// again. As is the facade entry-point (empty means the runtime applies its
// default), from which the runtime derives the /v1/<as> route.
type ConsumedAPI struct {
	ID       string `json:"id"`
	Module   string `json:"module"`
	Service  string `json:"service"`
	Endpoint string `json:"endpoint"`
	Protocol string `json:"protocol"`
	As       string `json:"as,omitempty"`
}

// ConsumedAPIs returns the runtime projection of the solution's api.consumes, in
// canonical (id-sorted) order. Nil when the solution consumes no APIs.
func (m *Manifest) ConsumedAPIs() []ConsumedAPI {
	if len(m.API.Consumes) == 0 {
		return nil
	}
	out := make([]ConsumedAPI, 0, len(m.API.Consumes))
	for _, declaration := range m.API.Consumes {
		out = append(out, ConsumedAPI{
			ID:       declaration.ID,
			Module:   declaration.Module,
			Service:  declaration.Service,
			Endpoint: declaration.Endpoint,
			Protocol: declaration.Protocol,
			As:       declaration.As,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ConsumedAPIsEnvValue encodes the consumed APIs as the CODEFLY__API_CONSUMES
// value. It returns the empty string when the solution consumes no APIs, so a
// caller injects the variable only for solutions that declare api.consumes.
func (m *Manifest) ConsumedAPIsEnvValue() (string, error) {
	consumed := m.ConsumedAPIs()
	if len(consumed) == 0 {
		return "", nil
	}
	encoded, err := json.Marshal(consumed)
	if err != nil {
		return "", fmt.Errorf("encode api.consumes: %w", err)
	}
	return string(encoded), nil
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
