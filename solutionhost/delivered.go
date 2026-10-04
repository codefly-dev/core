package solutionhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
)

// BundleVerifier is the caller's attestation check. Core does not implement
// one and never will: signing is keyless over a workload identity, verifying
// is sigstore-go against an identity policy and a trust root the verifier
// holds, and neither belongs in a library every binary imports.
//
// What core owns is the ORDERING. Before this interface existed, Parse and
// ParseAuthority returned the same types *FromVerified returned, and Admit and
// Activate accepted them — so "verified" was part of a function name and
// nothing more, and a host that forgot to verify a carrier had no way to find
// out. The workcontext half of this same change uses distinct types
// (*Authenticated, *Inspected, *Verified) for exactly this reason; the
// document half was still relying on a naming convention.
type BundleVerifier interface {
	// VerifyBundle checks the bundle over exactly these payload bytes and
	// returns the SIGNER IDENTITY it attests — for keyless signing, the
	// certificate's SAN, which is what a host maps to the domains that signer
	// may deliver.
	//
	// It returns an error when the attestation does not hold. Core treats any
	// error as "not delivered" and never inspects it.
	VerifyBundle(ctx context.Context, payload []byte, bundle json.RawMessage) (signer string, err error)
}

// Delivered is a presence document whose carrier was verified, together with
// the signer identity that was attested.
//
// Its fields are unexported and there is no exported constructor other than
// VerifyDelivered, so a document that reached Admit has been through a
// BundleVerifier. That is the same property *workcontext.Verified has, held
// the same way: the compiler, rather than a convention about names.
// It stores the ATTESTED PAYLOAD rather than a parsed document, and every
// reader re-derives from those bytes. The first version held a
// *SolutionHostBinding and handed that pointer out, which made the
// verification real and what was admitted afterwards MUTABLE. Measured, not
// hypothetical:
//
//	delivered.Document().Generation += 7
//	host.Admit(delivered)   // consumed generation 11; the attestation covered 4
//
// A caller did not have to be malicious — anything holding the document for
// its own bookkeeping and normalising a field would do it, and nothing
// anywhere would say so.
type Delivered struct {
	payload []byte
	signer  string
}

// Document re-derives the presence document from the attested bytes and
// returns a FRESH value each call, so a caller mutating what it gets back
// changes nothing Admit will consume.
//
// It returns an error rather than nil-on-failure because a reader that
// silently returned nothing would be the same class of defect one level down.
// In practice it cannot fail: VerifyDelivered parsed these bytes already.
func (d *Delivered) Document() (*SolutionHostBinding, error) {
	return PresenceFromVerified(d.payload)
}

// Payload is the attested bytes, copied. The digest a host stores is over
// these, never over anything a caller has held since.
func (d *Delivered) Payload() []byte { return bytes.Clone(d.payload) }

// DeliveredBy is the identity the caller's attestation check named. It is not
// a signing surface: core holds no key and attests nothing, and this is a
// string the CALLER handed it.
func (d *Delivered) DeliveredBy() string { return d.signer }

// DeliveredAuthority is an authority document whose carrier was verified. It
// stores the attested payload for the reason Delivered does.
type DeliveredAuthority struct {
	payload []byte
	signer  string
}

// Document re-derives the authority document from the attested bytes.
func (d *DeliveredAuthority) Document() (*AuthorityDocument, error) {
	return AuthorityFromVerified(d.payload)
}

// Payload is the attested bytes, copied.
func (d *DeliveredAuthority) Payload() []byte { return bytes.Clone(d.payload) }

// DeliveredBy is the identity the caller's attestation check named.
func (d *DeliveredAuthority) DeliveredBy() string { return d.signer }

// VerifyDelivered reads a signed carrier, hands its payload and bundle to the
// caller's BundleVerifier, and returns the presence document only when that
// verifier accepts.
//
// The order is the point and it is not negotiable from outside: the bundle is
// checked before the payload is turned into a document, so there is no
// sequence of exported calls that produces a Delivered without an attestation
// having held over exactly those bytes.
func VerifyDelivered(ctx context.Context, carrier *Signed, verifier BundleVerifier) (*Delivered, error) {
	signer, payload, err := verifyCarrier(ctx, carrier, verifier)
	if err != nil {
		return nil, err
	}
	// Parsed here so a carrier that yields no document is refused at the
	// boundary rather than at every reader, and DISCARDED: what is stored is
	// the attested bytes.
	if _, err := PresenceFromVerified(payload); err != nil {
		return nil, err
	}
	return &Delivered{payload: bytes.Clone(payload), signer: signer}, nil
}

// VerifyDeliveredAuthority is VerifyDelivered for an authority document.
func VerifyDeliveredAuthority(ctx context.Context, carrier *Signed, verifier BundleVerifier) (*DeliveredAuthority, error) {
	signer, payload, err := verifyCarrier(ctx, carrier, verifier)
	if err != nil {
		return nil, err
	}
	if _, err := AuthorityFromVerified(payload); err != nil {
		return nil, err
	}
	return &DeliveredAuthority{payload: bytes.Clone(payload), signer: signer}, nil
}

func verifyCarrier(ctx context.Context, carrier *Signed, verifier BundleVerifier) (string, []byte, error) {
	if carrier == nil {
		return "", nil, fmt.Errorf("%w: no carrier", ErrUnsigned)
	}
	// The carrier's own invariants. VerifyDelivered used to skip them: a
	// hand-built Signed went straight to the bundle verifier, so the schema
	// and the bundle's shape were enforced only on the ParseSigned path.
	if err := carrier.validate(); err != nil {
		return "", nil, err
	}
	if verifier == nil {
		return "", nil, fmt.Errorf("%w: no bundle verifier; core verifies no attestation and will not treat its absence as one holding", ErrUnsigned)
	}
	signer, err := verifier.VerifyBundle(ctx, carrier.Document, carrier.Bundle)
	if err != nil {
		return "", nil, fmt.Errorf("%w: the attestation does not hold: %v", ErrUnsigned, err)
	}
	if signer == "" {
		return "", nil, fmt.Errorf("%w: the bundle verifier named no signer identity, so nothing can be mapped to a domain", ErrUnsigned)
	}
	return signer, carrier.Document, nil
}
