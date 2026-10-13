package solutionhost

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/codefly-dev/core/resources"
)

// EndpointDeliverySchema is the CLI's replaceable host route projection. This
// is a transport carrier, not a presence or authority document: the protected
// delivery writer owns it, and the SDK joins it to the host's applied presence.
const EndpointDeliverySchema = "codefly/endpoint-delivery/v1"

// EndpointDeliveryMaxBytes bounds one component's projection before decoding.
const EndpointDeliveryMaxBytes = 1 << 20

// EndpointDelivery carries the existing endpoint encoding separately from the
// address-free presence document. PresenceDigest joins the complete applied
// declaration, including its target, generation and immutable release identity.
type EndpointDelivery struct {
	Schema         string            `yaml:"schema" json:"schema"`
	PresenceDigest string            `yaml:"presence_digest" json:"presence_digest"`
	Carriers       map[string]string `yaml:"carriers" json:"carriers"`
}

// EndpointDeliveryFilename is a stable, path-safe name for this binding on its
// host. A new generation replaces the same file; a different host or binding
// cannot select it. The filename grants no authority: ParseEndpointDelivery
// must still compare the contents against the applied document.
func EndpointDeliveryFilename(document *SolutionHostBinding) (string, error) {
	if err := document.Validate(); err != nil {
		return "", err
	}
	identity, err := json.Marshal([]string{document.Host.Coordinate, document.Host.Component, document.Binding})
	if err != nil {
		return "", err
	}
	return strings.TrimPrefix(digestOf(identity), "sha256:") + ".json", nil
}

// MarshalEndpointDelivery writes the CLI-resolved subset of this component's
// endpoints. It reuses the environment carrier keys and address parser, and
// never infers an endpoint absent from the supplied presence.
func MarshalEndpointDelivery(document *SolutionHostBinding, carriers map[string]string) ([]byte, error) {
	digest, err := document.Digest()
	if err != nil {
		return nil, err
	}
	projection := EndpointDelivery{Schema: EndpointDeliverySchema, PresenceDigest: digest, Carriers: maps.Clone(carriers)}
	if projection.Carriers == nil {
		projection.Carriers = map[string]string{}
	}
	raw, err := json.Marshal(projection)
	if err != nil {
		return nil, err
	}
	if _, err := ParseEndpointDelivery(raw, document); err != nil {
		return nil, err
	}
	return raw, nil
}

// ParseEndpointDelivery refuses stale, future, cross-target and rewritten
// same-generation projections by matching the entire canonical presence hash.
// It is strict about duplicate/unknown keys, nulls and trailing documents, as
// every delivery reader is. Removing a projection never authorizes fallback.
func ParseEndpointDelivery(raw []byte, document *SolutionHostBinding) (*EndpointDelivery, error) {
	if len(raw) > EndpointDeliveryMaxBytes {
		return nil, fmt.Errorf("%w: endpoint delivery exceeds its byte limit", ErrInvalid)
	}
	digest, err := document.Digest()
	if err != nil {
		return nil, err
	}
	projection, err := decodeStrict[EndpointDelivery](raw, "endpoint delivery", EndpointDeliverySchema, "", nil)
	if err != nil {
		return nil, err
	}
	if document.Removed || projection.PresenceDigest != digest {
		return nil, fmt.Errorf("%w: endpoint delivery does not name the active applied presence", ErrInvalid)
	}
	if projection.Carriers == nil || len(projection.Carriers) > len(document.Endpoints) {
		return nil, fmt.Errorf("%w: endpoint delivery must declare its endpoint subset", ErrInvalid)
	}
	declared := make(map[string]bool, len(document.Endpoints))
	for _, endpoint := range document.Endpoints {
		key := resources.EndpointAsEnvironmentVariableKey(&resources.EndpointInformation{
			Module: endpoint.Module, Service: endpoint.Service, Name: endpoint.Name, API: endpoint.API,
		})
		if declared[key] {
			return nil, fmt.Errorf("%w: declared endpoints collide in carrier encoding", ErrInvalid)
		}
		declared[key] = true
	}
	for key, address := range projection.Carriers {
		if !declared[key] {
			return nil, fmt.Errorf("%w: endpoint delivery names an undeclared endpoint", ErrInvalid)
		}
		if address == "" || len(address) > 4096 {
			return nil, fmt.Errorf("%w: endpoint delivery address is empty or too long", ErrInvalid)
		}
		if _, err := resources.ParseAddress(address); err != nil {
			return nil, fmt.Errorf("%w: endpoint delivery address is malformed", ErrInvalid)
		}
	}
	return projection, nil
}
