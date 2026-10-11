package solutionhost

import (
	"bytes"
	"context"
	"fmt"
)

// Delivered is a presence document whose carrier was verified, together with
// the pinned key identity that was attested.
//
// Its fields are unexported and there is no exported constructor other than
// VerifyDelivered, so a document that reached Admit has been through the
// pinned-key Verifier. That is the same property *workcontext.Verified has, held
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
	signer  SignerIdentity
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

// DeliveredBy is the ID of the pinned key that signed this document.
func (d *Delivered) DeliveredBy() string { return d.signer.KeyID }

// Signer returns the verified key ID and its kind from the host's pins.
func (d *Delivered) Signer() SignerIdentity { return d.signer }

// DeliveredAuthority is an authority document whose carrier was verified. It
// stores the attested payload for the reason Delivered does.
type DeliveredAuthority struct {
	payload []byte
	signer  SignerIdentity
}

// Document re-derives the authority document from the attested bytes.
func (d *DeliveredAuthority) Document() (*AuthorityDocument, error) {
	return AuthorityFromVerified(d.payload)
}

// Payload is the attested bytes, copied.
func (d *DeliveredAuthority) Payload() []byte { return bytes.Clone(d.payload) }

// DeliveredBy is the ID of the pinned key that signed this document.
func (d *DeliveredAuthority) DeliveredBy() string { return d.signer.KeyID }

// Signer returns the verified key ID and its kind from the host's pins.
func (d *DeliveredAuthority) Signer() SignerIdentity { return d.signer }

// VerifyDelivered verifies a carrier offline against the host's pinned keys,
// using only the Go standard library, with no third-party service. It then
// checks the canonical presence document and the key's ownership domains.
// It returns no Delivered value unless all three checks pass.
func VerifyDelivered(ctx context.Context, carrier *Signed, verifier *Verifier) (*Delivered, error) {
	signer, payload, err := verifyCarrier(ctx, carrier, verifier)
	if err != nil {
		return nil, err
	}
	// Parsed here so a carrier that yields no document is refused at the
	// boundary rather than at every reader, and DISCARDED: what is stored is
	// the attested bytes.
	document, err := PresenceFromVerified(payload)
	if err != nil {
		return nil, err
	}
	if err := verifier.checkDomain(signer, document.OwnershipDomain); err != nil {
		return nil, err
	}
	return &Delivered{payload: bytes.Clone(payload), signer: signer}, nil
}

// VerifyDeliveredAuthority is VerifyDelivered for an authority document.
func VerifyDeliveredAuthority(ctx context.Context, carrier *Signed, verifier *Verifier) (*DeliveredAuthority, error) {
	signer, payload, err := verifyCarrier(ctx, carrier, verifier)
	if err != nil {
		return nil, err
	}
	document, err := AuthorityFromVerified(payload)
	if err != nil {
		return nil, err
	}
	if err := verifier.checkDomain(signer, document.OwnershipDomain); err != nil {
		return nil, err
	}
	return &DeliveredAuthority{payload: bytes.Clone(payload), signer: signer}, nil
}

func verifyCarrier(ctx context.Context, carrier *Signed, verifier *Verifier) (SignerIdentity, []byte, error) {
	if carrier == nil {
		return SignerIdentity{}, nil, fmt.Errorf("%w: no carrier", ErrUnsigned)
	}
	// The carrier's own invariants. VerifyDelivered used to skip them: a
	// hand-built Signed went straight to the verifier, so the carrier shape
	// was enforced only on the ParseSigned path.
	if err := carrier.validate(); err != nil {
		return SignerIdentity{}, nil, err
	}
	if verifier == nil {
		return SignerIdentity{}, nil, fmt.Errorf("%w: no pinned-key verifier", ErrUnsigned)
	}
	signer, err := verifier.VerifyEnvelope(ctx, carrier.Document, carrier.Envelope)
	if err != nil {
		return SignerIdentity{}, nil, fmt.Errorf("%w: the attestation does not hold: %v", ErrUnsigned, err)
	}
	return signer, carrier.Document, nil
}
