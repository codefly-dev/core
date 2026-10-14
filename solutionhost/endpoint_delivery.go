package solutionhost

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/codefly-dev/core/resources"
)

// EndpointDeliverySchema is the CLI's replaceable host route projection. This
// is a transport carrier, not a presence or authority document: the protected
// delivery writer owns it, and the SDK joins it to the host's applied presence.
const EndpointDeliverySchema = "codefly/endpoint-delivery/v1"

// EndpointDeliveryMaxBytes bounds one component's projection before decoding.
const EndpointDeliveryMaxBytes = 1 << 20

// ErrEndpointsUnapplied means a verified endpoint delivery names a generation
// this host has not applied: no presence for the binding, or a generation
// other than the applied one. A delivery is held to the applied declaration,
// never kept for one that may arrive.
var ErrEndpointsUnapplied = errors.New("endpoint delivery names no applied presence on this host")

// EndpointDelivery carries the existing endpoint encoding separately from the
// address-free presence document. It names the applied declaration it joins —
// the host target, the binding, the generation, the ownership domain and the
// complete canonical presence digest — so a host verifies and stores a SIGNED
// delivery by those facts alone, without reading an address, and a resolver
// refuses one that names any other applied presence. The presence itself
// stays address-free: this is the one document an address travels in.
type EndpointDelivery struct {
	Schema          string            `yaml:"schema" json:"schema"`
	Host            HostTarget        `yaml:"host" json:"host"`
	Binding         string            `yaml:"binding" json:"binding"`
	Generation      uint64            `yaml:"generation" json:"generation"`
	OwnershipDomain string            `yaml:"ownership_domain" json:"ownership_domain"`
	PresenceDigest  string            `yaml:"presence_digest" json:"presence_digest"`
	Carriers        map[string]string `yaml:"carriers" json:"carriers"`
}

// EndpointDeliveryIdentity is the applied declaration a delivery names,
// without its addresses: what a host verifies, joins and stores it by.
type EndpointDeliveryIdentity struct {
	Host            HostTarget
	Binding         string
	Generation      uint64
	OwnershipDomain string
	PresenceDigest  string
}

// Key is the canonical storage identity of a binding's delivery on its host:
// a digest of the host target and the binding, so a later generation
// replaces the same slot and another host's or binding's delivery cannot
// select it. It is the digest EndpointDeliveryFilename names a file by, so a
// host's durable slot and a native file agree, and it grants nothing: a
// stored delivery is joined to the applied presence on every read.
func (identity EndpointDeliveryIdentity) Key() string {
	return endpointDeliveryKey(identity.Host, identity.Binding)
}

func endpointDeliveryKey(host HostTarget, binding string) string {
	// A []string marshals without error.
	identity, _ := json.Marshal([]string{host.Coordinate, host.Component, binding})
	return strings.TrimPrefix(digestOf(identity), "sha256:")
}

// Identity is what this delivery names, read without its addresses.
func (delivery *EndpointDelivery) Identity() EndpointDeliveryIdentity {
	return EndpointDeliveryIdentity{
		Host: delivery.Host, Binding: delivery.Binding, Generation: delivery.Generation,
		OwnershipDomain: delivery.OwnershipDomain, PresenceDigest: delivery.PresenceDigest,
	}
}

// CanonicalBytes is the one encoding of a delivery — keys sorted at every
// depth, as a presence document is encoded — and the bytes SignEnvelope signs.
func (delivery *EndpointDelivery) CanonicalBytes() ([]byte, error) {
	return canonicalJSON(delivery)
}

// validateIdentity holds the facts a host stores a delivery by to the shapes
// the presence document holds them to, so a slot is never keyed by nothing.
func (delivery *EndpointDelivery) validateIdentity() error {
	switch {
	case delivery.Host.Coordinate == "" || delivery.Host.Component == "":
		return fmt.Errorf("%w: endpoint delivery must name its host coordinate and component", ErrInvalid)
	case !bindingPattern.MatchString(delivery.Binding):
		return fmt.Errorf("%w: endpoint delivery binding %q is invalid", ErrInvalid, delivery.Binding)
	case delivery.Generation == 0:
		return fmt.Errorf("%w: endpoint delivery must name an applied generation", ErrInvalid)
	case !namePattern.MatchString(delivery.OwnershipDomain):
		return fmt.Errorf("%w: endpoint delivery ownership domain %q is invalid", ErrInvalid, delivery.OwnershipDomain)
	case !digestPattern.MatchString(delivery.PresenceDigest):
		return fmt.Errorf("%w: endpoint delivery must carry the canonical presence digest", ErrInvalid)
	}
	return nil
}

// EndpointDeliveryFilename is a stable, path-safe name for this binding on its
// host. A new generation replaces the same file; a different host or binding
// cannot select it. The filename grants no authority: ParseEndpointDelivery
// must still compare the contents against the applied document.
func EndpointDeliveryFilename(document *SolutionHostBinding) (string, error) {
	if err := document.Validate(); err != nil {
		return "", err
	}
	return endpointDeliveryKey(document.Host, document.Binding) + ".json", nil
}

// MarshalEndpointDelivery writes the CLI-resolved subset of this component's
// endpoints as the delivery's canonical bytes, naming the applied presence it
// joins. It reuses the environment carrier keys and address parser, and never
// infers an endpoint absent from the supplied presence. The bytes are what a
// native writer places in its file and what SignEnvelope signs for a host.
func MarshalEndpointDelivery(document *SolutionHostBinding, carriers map[string]string) ([]byte, error) {
	digest, err := document.Digest()
	if err != nil {
		return nil, err
	}
	projection := EndpointDelivery{
		Schema: EndpointDeliverySchema, Host: document.Host, Binding: document.Binding, Generation: document.Generation,
		OwnershipDomain: document.OwnershipDomain, PresenceDigest: digest, Carriers: maps.Clone(carriers),
	}
	if projection.Carriers == nil {
		projection.Carriers = map[string]string{}
	}
	raw, err := projection.CanonicalBytes()
	if err != nil {
		return nil, err
	}
	if _, err := ParseEndpointDelivery(raw, document); err != nil {
		return nil, err
	}
	return raw, nil
}

func decodeEndpointDelivery(raw []byte) (*EndpointDelivery, error) {
	if len(raw) > EndpointDeliveryMaxBytes {
		return nil, fmt.Errorf("%w: endpoint delivery exceeds its byte limit", ErrInvalid)
	}
	projection, err := decodeStrict[EndpointDelivery](raw, "endpoint delivery", EndpointDeliverySchema, "", nil)
	if err != nil {
		return nil, err
	}
	if err := projection.validateIdentity(); err != nil {
		return nil, err
	}
	return projection, nil
}

// ParseEndpointDelivery refuses stale, future, cross-target and rewritten
// same-generation projections by matching the entire canonical presence hash
// and every identity fact the delivery names. It is strict about
// duplicate/unknown keys, nulls and trailing documents, as every delivery
// reader is. Removing a projection never authorizes fallback.
func ParseEndpointDelivery(raw []byte, document *SolutionHostBinding) (*EndpointDelivery, error) {
	digest, err := document.Digest()
	if err != nil {
		return nil, err
	}
	projection, err := decodeEndpointDelivery(raw)
	if err != nil {
		return nil, err
	}
	if document.Removed || projection.PresenceDigest != digest || projection.Host != document.Host ||
		projection.Binding != document.Binding || projection.Generation != document.Generation ||
		projection.OwnershipDomain != document.OwnershipDomain {
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

// EndpointDeliveryFromVerified turns payload bytes the caller HAS ALREADY
// VERIFIED into a delivery, reading its identity and nothing of its
// addresses: strict decoding, the schema, and the canonical round-trip, as
// PresenceFromVerified does. It verifies no signature. Joining the delivery
// to an applied presence and reading its addresses are ParseEndpointDelivery's.
func EndpointDeliveryFromVerified(payload []byte) (*EndpointDelivery, error) {
	return fromVerified(payload, decodeEndpointDelivery, EndpointDeliverySchema, func(delivery *EndpointDelivery) ([]byte, error) {
		return delivery.CanonicalBytes()
	})
}

// DeliveredEndpoints is an endpoint delivery whose carrier was verified, held
// as its attested bytes for the reason Delivered is. A host stores these
// bytes opaquely under Identity().Key() once AdmitEndpoints has joined them to
// the applied presence; it never reads an address from them, and a resolver
// re-verifies the same bytes before it does.
type DeliveredEndpoints struct {
	payload []byte
	signer  SignerIdentity
}

// Identity re-derives what the delivery names from the attested bytes, a
// fresh value each call, without reading an address.
func (d *DeliveredEndpoints) Identity() (EndpointDeliveryIdentity, error) {
	delivery, err := EndpointDeliveryFromVerified(d.payload)
	if err != nil {
		return EndpointDeliveryIdentity{}, err
	}
	return delivery.Identity(), nil
}

// Payload is the attested bytes, copied: what a host stores and hands on.
func (d *DeliveredEndpoints) Payload() []byte { return bytes.Clone(d.payload) }

// DeliveredBy is the ID of the pinned key that signed this delivery.
func (d *DeliveredEndpoints) DeliveredBy() string { return d.signer.KeyID }

// Signer returns the verified key ID and its kind from the host's pins.
func (d *DeliveredEndpoints) Signer() SignerIdentity { return d.signer }

// VerifyDeliveredEndpoints is VerifyDelivered for an endpoint delivery: the
// same offline pinned-key verification, the canonical document, and the
// key's ownership domains against the domain the delivery names. It returns
// no DeliveredEndpoints unless all three checks pass, and reads no address.
func VerifyDeliveredEndpoints(ctx context.Context, carrier *Signed, verifier *Verifier) (*DeliveredEndpoints, error) {
	signer, payload, err := verifyCarrier(ctx, carrier, verifier)
	if err != nil {
		return nil, err
	}
	delivery, err := EndpointDeliveryFromVerified(payload)
	if err != nil {
		return nil, err
	}
	if err := verifier.checkDomain(signer, delivery.OwnershipDomain); err != nil {
		return nil, err
	}
	return &DeliveredEndpoints{payload: bytes.Clone(payload), signer: signer}, nil
}

// AdmitEndpoints joins a verified endpoint delivery to this host's applied
// state and returns the storage key the host keeps its attested bytes under.
// It reads the delivery's identity and never an address: the host target must
// be this host, the binding must have an applied generation, and the delivery
// must name exactly that generation, its digest and its domain. A delivery for
// a generation not applied, or no longer applied, is refused rather than held,
// as is one over a tombstone. The signer's domains were checked by
// VerifyDeliveredEndpoints; the applied record's domain is checked here.
func (host Host) AdmitEndpoints(delivered *DeliveredEndpoints) (string, error) {
	if delivered == nil {
		return "", fmt.Errorf("%w: no delivered endpoints", ErrUnsigned)
	}
	if host.Coordinate == "" {
		return "", fmt.Errorf("%w: endpoints are admitted by a named host", ErrWrongHost)
	}
	identity, err := delivered.Identity()
	if err != nil {
		return "", err
	}
	if identity.Host.Coordinate != host.Coordinate {
		return "", fmt.Errorf("%w: endpoint delivery targets %q, this host is %q", ErrWrongHost, identity.Host.Coordinate, host.Coordinate)
	}
	records, err := host.appliedByBinding()
	if err != nil {
		return "", err
	}
	record, applied := records[identity.Binding]
	switch {
	case !applied:
		return "", fmt.Errorf("%w: binding %q", ErrEndpointsUnapplied, identity.Binding)
	case record.Removed:
		return "", fmt.Errorf("%w: binding %q", ErrTombstoned, identity.Binding)
	case identity.Generation < record.Generation:
		return "", fmt.Errorf("%w: endpoint delivery names generation %d of binding %q, the host applied %d", ErrStaleGeneration, identity.Generation, identity.Binding, record.Generation)
	case identity.Generation > record.Generation:
		return "", fmt.Errorf("%w: endpoint delivery names generation %d of binding %q, the host applied %d", ErrEndpointsUnapplied, identity.Generation, identity.Binding, record.Generation)
	case identity.PresenceDigest != record.Digest:
		return "", fmt.Errorf("%w: endpoint delivery names binding %q generation %d as %s, the host applied %s", ErrRewrittenGeneration, identity.Binding, identity.Generation, identity.PresenceDigest, record.Digest)
	case identity.OwnershipDomain != record.Domain:
		return "", fmt.Errorf("%w: endpoint delivery names domain %q, binding %q was applied under %q", ErrWrongDomain, identity.OwnershipDomain, identity.Binding, record.Domain)
	case !slices.Contains(host.DomainsBySigner[delivered.DeliveredBy()], identity.OwnershipDomain):
		return "", fmt.Errorf("%w: endpoint delivery for domain %q was signed by %q, which this host does not accept for it", ErrWrongDomain, identity.OwnershipDomain, delivered.DeliveredBy())
	}
	return identity.Key(), nil
}

// ResolveSignedEndpointDelivery is the resolver's entry: it verifies a signed
// delivery against the pinned keys, then joins it to the applied presence the
// resolver holds and reads its addresses — ParseEndpointDelivery over the
// attested bytes. A delivery that fails any step resolves nothing, and the
// resolver never falls back to an environment carrier. The raw value is the
// signed carrier as MarshalSigned wrote it, handed to the resolver explicitly;
// no directory is read.
func ResolveSignedEndpointDelivery(ctx context.Context, raw []byte, verifier *Verifier, document *SolutionHostBinding) (*EndpointDelivery, error) {
	carrier, err := ParseSigned(raw)
	if err != nil {
		return nil, err
	}
	delivered, err := VerifyDeliveredEndpoints(ctx, carrier, verifier)
	if err != nil {
		return nil, err
	}
	return ParseEndpointDelivery(delivered.Payload(), document)
}
