package solutionhost_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

func endpointDelivery(t *testing.T) (*solutionhost.SolutionHostBinding, string, []byte) {
	t.Helper()
	document := valid(t)
	endpoint := document.Endpoints[0]
	key := resources.EndpointAsEnvironmentVariableKey(&resources.EndpointInformation{
		Module: endpoint.Module, Service: endpoint.Service, Name: endpoint.Name, API: endpoint.API,
	})
	raw, err := solutionhost.MarshalEndpointDelivery(document, map[string]string{key: "http://component.example.svc.cluster.local:8080"})
	require.NoError(t, err)
	return document, key, raw
}

func TestEndpointDeliveryJoinsTheWholeAppliedPresence(t *testing.T) {
	document, key, raw := endpointDelivery(t)
	projection, err := solutionhost.ParseEndpointDelivery(raw, document)
	require.NoError(t, err)
	require.NotEmpty(t, projection.Carriers[key])
	filename, err := solutionhost.EndpointDeliveryFilename(document)
	require.NoError(t, err)
	require.Regexp(t, `^[a-f0-9]{64}\.json$`, filename)
	for name, change := range map[string]func(*solutionhost.SolutionHostBinding){
		"generation":                      func(d *solutionhost.SolutionHostBinding) { d.Generation++ },
		"host":                            func(d *solutionhost.SolutionHostBinding) { d.Host.Coordinate = "another-host" },
		"component":                       func(d *solutionhost.SolutionHostBinding) { d.Host.Component = "another-component" },
		"binding":                         func(d *solutionhost.SolutionHostBinding) { d.Binding = "another-binding" },
		"same generation changed ceiling": func(d *solutionhost.SolutionHostBinding) { d.EnvelopeRevision++ },
	} {
		t.Run(name, func(t *testing.T) {
			changed := *document
			change(&changed)
			require.NoError(t, changed.Validate())
			_, err := solutionhost.ParseEndpointDelivery(raw, &changed)
			require.ErrorContains(t, err, "active applied presence")
		})
	}
	document.Generation++
	next, err := solutionhost.EndpointDeliveryFilename(document)
	require.NoError(t, err)
	require.Equal(t, filename, next, "a generation replaces the same projected file")
}

func TestEndpointDeliveryRefusesAmbiguousOrUndeclaredCarriers(t *testing.T) {
	document, key, raw := endpointDelivery(t)
	var projection solutionhost.EndpointDelivery
	require.NoError(t, json.Unmarshal(raw, &projection))
	for name, mutate := range map[string]func() []byte{
		"duplicate key": func() []byte {
			return []byte(strings.Replace(string(raw), `"schema":`, `"schema":"codefly/endpoint-delivery/v1","schema":`, 1))
		},
		"unknown":            func() []byte { return append([]byte(`{"extra":true,`), raw[1:]...) },
		"multiple documents": func() []byte { return append(append([]byte{}, raw...), []byte("\n---\n{}")...) },
		"null carriers": func() []byte {
			p := projection
			p.Carriers = nil
			b, err := json.Marshal(p)
			require.NoError(t, err)
			return b
		},
		"undeclared": func() []byte {
			p := projection
			p.Carriers = map[string]string{key + "_OTHER": "http://another.example:8080"}
			b, err := json.Marshal(p)
			require.NoError(t, err)
			return b
		},
		"oversized": func() []byte { return []byte(strings.Repeat(" ", solutionhost.EndpointDeliveryMaxBytes+1)) },
	} {
		t.Run(name, func(t *testing.T) {
			_, err := solutionhost.ParseEndpointDelivery(mutate(), document)
			require.Error(t, err)
		})
	}
	raw, err := solutionhost.MarshalEndpointDelivery(document, nil)
	require.NoError(t, err)
	got, err := solutionhost.ParseEndpointDelivery(raw, document)
	require.NoError(t, err)
	require.Empty(t, got.Carriers, "an explicit empty subset grants no route")
}

// signedEndpointDelivery signs a delivery's canonical bytes with a fresh
// fixture key, as the delivery writer does, and returns the carrier as it
// travels, a verifier pinning that key for the document's domain, and the
// signer, for a verifier that pins the same key differently.
func signedEndpointDelivery(t *testing.T, document *solutionhost.SolutionHostBinding, raw []byte) ([]byte, *solutionhost.Verifier, *solutionhost.InMemorySigner) {
	t.Helper()
	signer := signerFor(t, fixtureDeliveredBy)
	envelope, err := solutionhost.SignEnvelope(raw, signer)
	require.NoError(t, err)
	carrier, err := solutionhost.Carrier(raw, envelope)
	require.NoError(t, err)
	encoded, err := solutionhost.MarshalSigned(carrier)
	require.NoError(t, err)
	return encoded, verifierFor(t, pinFor(t, signer, solutionhost.KeyKindRelease, document.OwnershipDomain)), signer
}

// A signed delivery binds its exact bytes to the applied presence it names.
// The host verifies it, reads its identity and joins it to the applied record
// without reading an address, and stores the attested bytes under a key a
// later generation replaces; the resolver verifies the same bytes again and
// only then reads the addresses. The presence stays address-free throughout.
func TestASignedEndpointDeliveryIsVerifiedStoredAndResolvedWithoutReadingAnAddress(t *testing.T) {
	ctx := context.Background()
	document, key, raw := endpointDelivery(t)
	encoded, verifier, _ := signedEndpointDelivery(t, document, raw)
	require.NotContains(t, string(presence(t, "valid")), "component.example.svc", "the presence carries no address")

	carrier, err := solutionhost.ParseSigned(encoded)
	require.NoError(t, err)
	delivered, err := solutionhost.VerifyDeliveredEndpoints(ctx, carrier, verifier)
	require.NoError(t, err)
	require.Equal(t, raw, delivered.Payload(), "the attested bytes are exactly what was written")
	require.Equal(t, fixtureDeliveredBy, delivered.DeliveredBy())

	identity, err := delivered.Identity()
	require.NoError(t, err)
	digest, err := document.Digest()
	require.NoError(t, err)
	require.Equal(t, solutionhost.EndpointDeliveryIdentity{
		Host: document.Host, Binding: document.Binding, Generation: document.Generation,
		OwnershipDomain: document.OwnershipDomain, PresenceDigest: digest,
	}, identity)

	applied, err := solutionhost.AppliedFrom(document)
	require.NoError(t, err)
	slot, err := appliedHost(applied).AdmitEndpoints(delivered)
	require.NoError(t, err)
	filename, err := solutionhost.EndpointDeliveryFilename(document)
	require.NoError(t, err)
	require.Equal(t, filename, slot+".json", "the host's slot and the native file name the same identity")
	require.Equal(t, slot, identity.Key())

	projection, err := solutionhost.ResolveSignedEndpointDelivery(ctx, encoded, verifier, document)
	require.NoError(t, err)
	require.Equal(t, "http://component.example.svc.cluster.local:8080", projection.Carriers[key])
	require.Equal(t, identity, projection.Identity())

	// The canonical bytes are the one encoding: re-encoding what was read
	// gives back exactly what was signed.
	again, err := projection.CanonicalBytes()
	require.NoError(t, err)
	require.Equal(t, raw, again)
}

func TestASignedEndpointDeliveryIsRefusedWhereverItsBindingFails(t *testing.T) {
	ctx := context.Background()
	document, _, raw := endpointDelivery(t)
	encoded, verifier, signer := signedEndpointDelivery(t, document, raw)
	applied, err := solutionhost.AppliedFrom(document)
	require.NoError(t, err)
	carrier, err := solutionhost.ParseSigned(encoded)
	require.NoError(t, err)
	delivered, err := solutionhost.VerifyDeliveredEndpoints(ctx, carrier, verifier)
	require.NoError(t, err)

	// Verification: a tampered carrier, an envelope made for the presence,
	// an unsigned delivery, a key not pinned for the domain, and bytes that
	// are not canonical never reach a host or a resolver.
	tampered := []byte(strings.Replace(string(encoded), ":8080", ":8081", 1))
	_, err = solutionhost.ResolveSignedEndpointDelivery(ctx, tampered, verifier, document)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	presencePayload, err := document.CanonicalBytes()
	require.NoError(t, err)
	presenceEnvelope, err := solutionhost.SignEnvelope(presencePayload, signer)
	require.NoError(t, err)
	_, err = solutionhost.VerifyDeliveredEndpoints(ctx, &solutionhost.Signed{Document: raw, Envelope: presenceEnvelope}, verifier)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	_, err = solutionhost.ResolveSignedEndpointDelivery(ctx, raw, verifier, document)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned, "a bare delivery is no signed delivery")
	elsewhere := verifierFor(t, pinFor(t, signer, solutionhost.KeyKindRelease, "beta"))
	_, err = solutionhost.VerifyDeliveredEndpoints(ctx, carrier, elsewhere)
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain)
	loose := append([]byte(" "), raw...)
	looseEnvelope, err := solutionhost.SignEnvelope(loose, signer)
	require.NoError(t, err)
	_, err = solutionhost.Carrier(loose, looseEnvelope)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned, "a non-canonical delivery is refused at the writer")
	_, err = solutionhost.EndpointDeliveryFromVerified(loose)
	require.ErrorIs(t, err, solutionhost.ErrNotCanonical)

	// Admission: the host joins the identity to its applied record.
	for name, scenario := range map[string]struct {
		host solutionhost.Host
		want error
	}{
		"another host":    {host: func() solutionhost.Host { h := appliedHost(applied); h.Coordinate = "example/prod/region-b"; return h }(), want: solutionhost.ErrWrongHost},
		"nothing applied": {host: appliedHost(), want: solutionhost.ErrEndpointsUnapplied},
		"older than applied": {host: func() solutionhost.Host {
			later := applied
			later.Generation++
			return appliedHost(later)
		}(), want: solutionhost.ErrStaleGeneration},
		"not yet applied": {host: func() solutionhost.Host {
			earlier := applied
			earlier.Generation--
			return appliedHost(earlier)
		}(), want: solutionhost.ErrEndpointsUnapplied},
		"rewritten generation": {host: func() solutionhost.Host {
			rewritten := applied
			rewritten.Digest = "sha256:" + strings.Repeat("0", 64)
			return appliedHost(rewritten)
		}(), want: solutionhost.ErrRewrittenGeneration},
		"another domain": {host: func() solutionhost.Host {
			other := applied
			other.Domain = "beta"
			return appliedHost(other)
		}(), want: solutionhost.ErrWrongDomain},
		"signer not accepted for the domain": {host: func() solutionhost.Host {
			h := appliedHost(applied)
			h.DomainsBySigner = map[string][]string{fixtureDeliveredBy: {"beta"}}
			return h
		}(), want: solutionhost.ErrWrongDomain},
		"tombstone": {host: func() solutionhost.Host {
			withdrawn := applied
			withdrawn.Removed, withdrawn.Routes = true, nil
			return appliedHost(withdrawn)
		}(), want: solutionhost.ErrTombstoned},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := scenario.host.AdmitEndpoints(delivered)
			require.ErrorIs(t, err, scenario.want)
		})
	}
	_, err = solutionhost.Host{}.AdmitEndpoints(delivered)
	require.ErrorIs(t, err, solutionhost.ErrWrongHost, "an unnamed host admits no endpoints")

	// Resolution: the resolver's own applied presence must be the one named.
	next := *document
	next.Generation++
	_, err = solutionhost.ResolveSignedEndpointDelivery(ctx, encoded, verifier, &next)
	require.ErrorContains(t, err, "active applied presence")
}
