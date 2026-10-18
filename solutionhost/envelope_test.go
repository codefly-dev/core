package solutionhost_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

// validEnvelope grants two principals on the test host. The grants are the
// host's shape, not Core's: Core only insists each is a JSON object.
func validEnvelope(t *testing.T) *solutionhost.ModuleEnvelope {
	t.Helper()
	envelope := &solutionhost.ModuleEnvelope{
		Schema:   solutionhost.SchemaModuleEnvelopeV1,
		Host:     solutionhost.HostTarget{Coordinate: testCoordinate, Domain: testDomain},
		Revision: 3,
		Principals: map[string]json.RawMessage{
			"wiki":    json.RawMessage(`{"scopes": ["modelservice.profiles:invoke"], "audience": "modelservice"}`),
			"indexer": json.RawMessage(`{}`),
		},
	}
	require.NoError(t, envelope.Validate())
	return envelope
}

func envelopePayload(t *testing.T, envelope *solutionhost.ModuleEnvelope) []byte {
	t.Helper()
	payload, err := envelope.CanonicalBytes()
	require.NoError(t, err)
	return payload
}

// Sign, write, read, verify: what publish signs with the cell's release key is
// what the host reads back, byte for byte, as the envelope that was signed.
func TestASignedEnvelopeRoundTripsThroughTheManifestsCarrier(t *testing.T) {
	envelope := validEnvelope(t)
	payload := envelopePayload(t, envelope)
	signed, err := solutionhost.SignEnvelope(payload, signerFor(t, testKeyID))
	require.NoError(t, err)
	var wire map[string]any
	require.NoError(t, json.Unmarshal(signed, &wire))
	require.Equal(t, solutionhost.PayloadTypeModuleEnvelope, wire["payloadType"])

	carrier, err := solutionhost.Carrier(payload, signed)
	require.NoError(t, err)
	written, err := solutionhost.MarshalSigned(carrier)
	require.NoError(t, err)
	read, err := solutionhost.ParseSigned(written)
	require.NoError(t, err)

	delivered, err := solutionhost.VerifyDeliveredEnvelope(context.Background(), read, testVerifier(t))
	require.NoError(t, err)
	require.Equal(t, payload, delivered.Payload())
	require.Equal(t, solutionhost.SignerIdentity{KeyID: testKeyID, Kind: solutionhost.KeyKindRelease}, delivered.Signer())
	got, err := delivered.Envelope()
	require.NoError(t, err)
	want, err := envelope.Digest()
	require.NoError(t, err)
	digest, err := got.Digest()
	require.NoError(t, err)
	require.Equal(t, want, digest)
	require.Equal(t, solutionhost.SchemaModuleEnvelopeV1, solutionhost.SchemaOf(payload))

	// The value handed out is a copy: changing it changes nothing delivered.
	got.Principals["wiki"] = json.RawMessage(`{"scopes":["everything"]}`)
	again, err := delivered.Envelope()
	require.NoError(t, err)
	require.JSONEq(t, `{"audience":"modelservice","scopes":["modelservice.profiles:invoke"]}`, string(again.Principals["wiki"]))

	// An envelope granting nothing is a statement, and travels the same way.
	empty := validEnvelope(t)
	empty.Principals = map[string]json.RawMessage{}
	_, err = solutionhost.VerifyDeliveredEnvelope(context.Background(), signedWith(t, envelopePayload(t, empty), signerFor(t, testKeyID)), testVerifier(t))
	require.NoError(t, err)
}

func TestAnEnvelopeIsAdmittedOnlyFromAPinnedReleaseKeyForItsDomain(t *testing.T) {
	payload := envelopePayload(t, validEnvelope(t))
	carrier := signedWith(t, payload, signerFor(t, testKeyID))

	// A person's key verifies the signature and is still refused: a developer
	// key never speaks for a host's authority grants.
	person := verifierFor(t, pinFor(t, signerFor(t, testKeyID), solutionhost.KeyKindPerson, testDomain))
	_, err := solutionhost.VerifyDeliveredEnvelope(context.Background(), carrier, person)
	require.ErrorIs(t, err, solutionhost.ErrEnvelopeSignerNotRelease)

	// A release key not pinned for the envelope's domain.
	otherDomain := verifierFor(t, pinFor(t, signerFor(t, testKeyID), solutionhost.KeyKindRelease, "other"))
	_, err = solutionhost.VerifyDeliveredEnvelope(context.Background(), carrier, otherDomain)
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain)

	// A key the host does not pin, and the pinned key ID over another key.
	_, err = solutionhost.VerifyDeliveredEnvelope(context.Background(), signedWith(t, payload, signerFor(t, "stranger")), testVerifier(t))
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	_, err = solutionhost.VerifyDeliveredEnvelope(context.Background(), signedWith(t, payload, differentKey(t, testKeyID)), testVerifier(t))
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)

	// No carrier, no verifier.
	_, err = solutionhost.VerifyDeliveredEnvelope(context.Background(), nil, testVerifier(t))
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	_, err = solutionhost.VerifyDeliveredEnvelope(context.Background(), carrier, nil)
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
}

// One carrier, two documents: each verifier accepts only its own, so a
// manifest can never be admitted as a host's grants, nor grants as presence.
func TestEachVerifierAcceptsOnlyItsOwnDocument(t *testing.T) {
	_, err := solutionhost.VerifyDeliveredEnvelope(context.Background(), signedValid(t), testVerifier(t))
	require.ErrorIs(t, err, solutionhost.ErrSchema)

	envelope := signedWith(t, envelopePayload(t, validEnvelope(t)), signerFor(t, testKeyID))
	_, err = solutionhost.VerifyDelivered(context.Background(), envelope, testVerifier(t))
	require.ErrorIs(t, err, solutionhost.ErrSchema)
}

// DSSE signs the payload type with the payload. A genuine signature made under
// one document type is refused for the other, whichever label the envelope
// then carries.
func TestASignatureIsBoundToItsDocumentType(t *testing.T) {
	signer := signerFor(t, testKeyID)
	verifier := testVerifier(t)
	for name, tc := range map[string]struct {
		carrier  *solutionhost.Signed
		signedAs string
	}{
		"envelope signed as a manifest": {
			carrier:  signedWith(t, envelopePayload(t, validEnvelope(t)), signer),
			signedAs: solutionhost.PayloadTypeModuleManifest,
		},
		"manifest signed as an envelope": {
			carrier:  signedValid(t),
			signedAs: solutionhost.PayloadTypeModuleEnvelope,
		},
	} {
		t.Run(name, func(t *testing.T) {
			document := tc.carrier.Document
			message := []byte(fmt.Sprintf("DSSEv1 %d %s %d %s", len(tc.signedAs), tc.signedAs, len(document), document))
			signature, err := signer.Sign(message)
			require.NoError(t, err)
			sig := base64.StdEncoding.EncodeToString(signature)

			// Labelled with the type it was signed under: the label disagrees
			// with the document's schema.
			labelled := changeEnvelope(t, tc.carrier, func(e *wireEnvelope) {
				e.PayloadType = tc.signedAs
				e.Signatures[0].Sig = sig
			})
			// Relabelled with the document's own type: the signature is over
			// the other type, so it does not verify.
			relabelled := changeEnvelope(t, tc.carrier, func(e *wireEnvelope) { e.Signatures[0].Sig = sig })
			for _, carrier := range []*solutionhost.Signed{labelled, relabelled} {
				_, err := solutionhost.VerifyDeliveredEnvelope(context.Background(), carrier, verifier)
				require.ErrorIs(t, err, solutionhost.ErrUnsigned)
				_, err = solutionhost.VerifyDelivered(context.Background(), carrier, verifier)
				require.ErrorIs(t, err, solutionhost.ErrUnsigned)
			}
		})
	}
}

// A payload that is not the canonical encoding of the envelope it decodes to
// is refused even under a genuine signature, as a manifest's is.
func TestOnlyACanonicalEnvelopeIsCarriedOrDelivered(t *testing.T) {
	canonical := string(envelopePayload(t, validEnvelope(t)))
	for name, payload := range map[string]string{
		"whitespace":             canonical[:1] + " " + canonical[1:],
		"top-level key order":    reorderTop(t, canonical),
		"grant key order":        strings.Replace(canonical, `{"audience":"modelservice","scopes":["modelservice.profiles:invoke"]}`, `{"scopes":["modelservice.profiles:invoke"],"audience":"modelservice"}`, 1),
		"duplicate top-level":    strings.Replace(canonical, `"revision":3`, `"revision":2,"revision":3`, 1),
		"duplicate key in grant": strings.Replace(canonical, `{"audience":"modelservice",`, `{"audience":"other","audience":"modelservice",`, 1),
		"trailing newline":       canonical + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			require.NotEqual(t, canonical, payload)
			_, err := solutionhost.EnvelopeFromVerified([]byte(payload))
			require.ErrorIs(t, err, solutionhost.ErrNotCanonical)

			signed, err := solutionhost.SignEnvelope([]byte(payload), signerFor(t, testKeyID))
			require.NoError(t, err, "the envelope signs bytes; the carrier is what insists they are canonical")
			_, err = solutionhost.Carrier([]byte(payload), signed)
			require.ErrorIs(t, err, solutionhost.ErrUnsigned)
			require.ErrorIs(t, err, solutionhost.ErrNotCanonical)
			_, err = solutionhost.VerifyDeliveredEnvelope(context.Background(), &solutionhost.Signed{Document: []byte(payload), Envelope: signed}, testVerifier(t))
			require.ErrorIs(t, err, solutionhost.ErrNotCanonical)
		})
	}

	for name, payload := range map[string]string{
		"unknown top-level field": strings.Replace(canonical, `"revision":3`, `"revision":3,"signer":"me"`, 1),
		"second document":         canonical + canonical,
		"not json":                "schema: codefly/module-envelope/v1",
		"too large":               canonical[:len(canonical)-1] + `,"pad":"` + strings.Repeat("x", solutionhost.EnvelopeMaxBytes) + `"}`,
	} {
		_, err := solutionhost.EnvelopeFromVerified([]byte(payload))
		require.ErrorIs(t, err, solutionhost.ErrEnvelopeInvalid, name)
	}

	// Neither document: the carrier says what it accepts.
	other := strings.Replace(canonical, solutionhost.SchemaModuleEnvelopeV1, "codefly/module-envelope/v2", 1)
	_, err := solutionhost.SignEnvelope([]byte(other), signerFor(t, testKeyID))
	require.ErrorIs(t, err, solutionhost.ErrSchema)
	require.ErrorContains(t, err, solutionhost.SchemaModuleEnvelopeV1)
	_, err = solutionhost.Carrier([]byte(other), json.RawMessage(`{"payloadType":"x"}`))
	require.ErrorIs(t, err, solutionhost.ErrUnsigned)
	require.ErrorIs(t, err, solutionhost.ErrSchema)
	_, err = solutionhost.EnvelopeFromVerified([]byte(other))
	require.ErrorIs(t, err, solutionhost.ErrSchema)
}

// reorderTop moves "schema" first, which is not name order.
func reorderTop(t *testing.T, canonical string) string {
	t.Helper()
	schema := `,"schema":"` + solutionhost.SchemaModuleEnvelopeV1 + `"`
	require.Contains(t, canonical, schema)
	return `{"schema":"` + solutionhost.SchemaModuleEnvelopeV1 + `",` + strings.Replace(canonical, schema, "", 1)[1:]
}

// A grant is opaque to Core, so canonicalisation must not reinterpret it: a
// number keeps its exact literal, however large.
func TestAGrantsNumbersSurviveCanonicalisation(t *testing.T) {
	envelope := validEnvelope(t)
	envelope.Principals["wiki"] = json.RawMessage(`{"quota": 12345678901234567890, "ratio": 1.50, "small": 1e-7}`)
	payload := envelopePayload(t, envelope)
	require.Contains(t, string(payload), `{"quota":12345678901234567890,"ratio":1.50,"small":1e-7}`)
	read, err := solutionhost.EnvelopeFromVerified(payload)
	require.NoError(t, err)
	require.Equal(t, `{"quota":12345678901234567890,"ratio":1.50,"small":1e-7}`, string(read.Principals["wiki"]))
}

func TestEnvelopeValidate(t *testing.T) {
	for name, tc := range map[string]struct {
		change func(*solutionhost.ModuleEnvelope)
		want   error
	}{
		"wrong schema":         {func(e *solutionhost.ModuleEnvelope) { e.Schema = solutionhost.SchemaModuleManifestV1 }, solutionhost.ErrSchema},
		"no schema":            {func(e *solutionhost.ModuleEnvelope) { e.Schema = "" }, solutionhost.ErrSchema},
		"bad coordinate":       {func(e *solutionhost.ModuleEnvelope) { e.Host.Coordinate = "Obin/Local" }, solutionhost.ErrEnvelopeInvalid},
		"no coordinate":        {func(e *solutionhost.ModuleEnvelope) { e.Host.Coordinate = "" }, solutionhost.ErrEnvelopeInvalid},
		"bad domain":           {func(e *solutionhost.ModuleEnvelope) { e.Host.Domain = "obin domain" }, solutionhost.ErrEnvelopeInvalid},
		"zero revision":        {func(e *solutionhost.ModuleEnvelope) { e.Revision = 0 }, solutionhost.ErrEnvelopeInvalid},
		"nil principals":       {func(e *solutionhost.ModuleEnvelope) { e.Principals = nil }, solutionhost.ErrEnvelopeInvalid},
		"path principal":       {func(e *solutionhost.ModuleEnvelope) { e.Principals["wiki/admin"] = json.RawMessage(`{}`) }, solutionhost.ErrEnvelopeInvalid},
		"uppercase principal":  {func(e *solutionhost.ModuleEnvelope) { e.Principals["Wiki"] = json.RawMessage(`{}`) }, solutionhost.ErrEnvelopeInvalid},
		"empty principal":      {func(e *solutionhost.ModuleEnvelope) { e.Principals[""] = json.RawMessage(`{}`) }, solutionhost.ErrEnvelopeInvalid},
		"64-byte principal":    {func(e *solutionhost.ModuleEnvelope) { e.Principals[strings.Repeat("a", 64)] = json.RawMessage(`{}`) }, solutionhost.ErrEnvelopeInvalid},
		"string grant":         {func(e *solutionhost.ModuleEnvelope) { e.Principals["wiki"] = json.RawMessage(`"all"`) }, solutionhost.ErrEnvelopeInvalid},
		"array grant":          {func(e *solutionhost.ModuleEnvelope) { e.Principals["wiki"] = json.RawMessage(`[{}]`) }, solutionhost.ErrEnvelopeInvalid},
		"null grant":           {func(e *solutionhost.ModuleEnvelope) { e.Principals["wiki"] = json.RawMessage(`null`) }, solutionhost.ErrEnvelopeInvalid},
		"absent grant":         {func(e *solutionhost.ModuleEnvelope) { e.Principals["wiki"] = nil }, solutionhost.ErrEnvelopeInvalid},
		"two objects in grant": {func(e *solutionhost.ModuleEnvelope) { e.Principals["wiki"] = json.RawMessage(`{}{}`) }, solutionhost.ErrEnvelopeInvalid},
		"broken grant":         {func(e *solutionhost.ModuleEnvelope) { e.Principals["wiki"] = json.RawMessage(`{"a":`) }, solutionhost.ErrEnvelopeInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			envelope := validEnvelope(t)
			tc.change(envelope)
			require.ErrorIs(t, envelope.Validate(), tc.want)
			_, err := envelope.CanonicalBytes()
			require.ErrorIs(t, err, tc.want)
		})
	}

	envelope := validEnvelope(t)
	envelope.Principals[strings.Repeat("a", 63)] = json.RawMessage(` {"padded": true} `)
	require.NoError(t, envelope.Validate(), "63 bytes is a principal, and whitespace around one object is still one object")
	var none *solutionhost.ModuleEnvelope
	require.ErrorIs(t, none.Validate(), solutionhost.ErrEnvelopeInvalid)
}

func TestAdmitEnvelope(t *testing.T) {
	at := func(revision uint64, change ...func(*solutionhost.ModuleEnvelope)) *solutionhost.ModuleEnvelope {
		envelope := validEnvelope(t)
		envelope.Revision = revision
		for _, c := range change {
			c(envelope)
		}
		return envelope
	}
	regrant := func(e *solutionhost.ModuleEnvelope) { e.Principals["wiki"] = json.RawMessage(`{"scopes":[]}`) }
	reordered := func(e *solutionhost.ModuleEnvelope) {
		e.Principals["wiki"] = json.RawMessage(`{"audience":"modelservice","scopes":["modelservice.profiles:invoke"]}`)
	}
	elsewhere := func(e *solutionhost.ModuleEnvelope) { e.Host.Coordinate = "obin/local/other" }
	otherDomain := func(e *solutionhost.ModuleEnvelope) { e.Host.Domain = "other" }

	for name, tc := range map[string]struct {
		coordinate          string
		previous, candidate *solutionhost.ModuleEnvelope
		want                solutionhost.Decision
		err                 error
	}{
		"first delivery":                     {testCoordinate, nil, at(1), solutionhost.DecisionAccept, nil},
		"replay":                             {testCoordinate, at(3), at(3), solutionhost.DecisionReplay, nil},
		"replay in another key order":        {testCoordinate, at(3), at(3, reordered), solutionhost.DecisionReplay, nil},
		"rewritten revision":                 {testCoordinate, at(3), at(3, regrant), "", solutionhost.ErrEnvelopeRewrittenRevision},
		"stale revision":                     {testCoordinate, at(3), at(2), "", solutionhost.ErrEnvelopeStaleRevision},
		"higher revision":                    {testCoordinate, at(3), at(4, regrant), solutionhost.DecisionAccept, nil},
		"candidate for another host":         {testCoordinate, nil, at(1, elsewhere), "", solutionhost.ErrWrongHost},
		"previous for another host":          {"obin/local/other", at(3), at(4, elsewhere), "", solutionhost.ErrWrongHost},
		"domain change, even higher":         {testCoordinate, at(3), at(9, otherDomain), "", solutionhost.ErrWrongDomain},
		"not a coordinate":                   {"Not A Host", nil, at(1), "", solutionhost.ErrEnvelopeInvalid},
		"invalid candidate":                  {testCoordinate, nil, at(0), "", solutionhost.ErrEnvelopeInvalid},
		"no candidate":                       {testCoordinate, at(3), nil, "", solutionhost.ErrEnvelopeInvalid},
		"invalid previous":                   {testCoordinate, at(0), at(1), "", solutionhost.ErrAppliedUnusable},
		"previous with an unreadable schema": {testCoordinate, at(3, func(e *solutionhost.ModuleEnvelope) { e.Schema = "x" }), at(4), "", solutionhost.ErrAppliedUnusable},
	} {
		t.Run(name, func(t *testing.T) {
			decision, err := solutionhost.AdmitEnvelope(tc.coordinate, tc.previous, tc.candidate)
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
				require.Empty(t, decision)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, decision)
		})
	}
}

// The first envelope is admitted under whatever domain it declares, from any
// release key pinned for that domain, and its domain then owns the host's
// envelope. Core cannot know which domain should; this pins that contract, so
// a host that does not pin the owner of its envelope and check it before
// admission is visibly relying on trust on first use.
func TestTheFirstEnvelopeIsAdmittedUnderAnyPinnedDomainSoTheHostPinsItsOwner(t *testing.T) {
	const owner, foreign = testDomain, "acme"
	acme := signerFor(t, "acme-release")
	verifier := verifierFor(t,
		pinFor(t, signerFor(t, testKeyID), solutionhost.KeyKindRelease, owner),
		pinFor(t, acme, solutionhost.KeyKindRelease, foreign))

	claim := validEnvelope(t)
	claim.Host.Domain = foreign
	claim.Revision = 1
	delivered, err := solutionhost.VerifyDeliveredEnvelope(context.Background(), signedWith(t, envelopePayload(t, claim), acme), verifier)
	require.NoError(t, err, "acme's key is a release key pinned for the domain the envelope declares")
	first, err := delivered.Envelope()
	require.NoError(t, err)
	decision, err := solutionhost.AdmitEnvelope(testCoordinate, nil, first)
	require.NoError(t, err)
	require.Equal(t, solutionhost.DecisionAccept, decision, "with nothing delivered, Core has no domain to hold the first envelope to")

	// From then on the first domain owns the host's envelope, whatever the
	// revision: the rightful owner is locked out.
	rightful := validEnvelope(t)
	rightful.Revision = 99
	_, err = solutionhost.AdmitEnvelope(testCoordinate, first, rightful)
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain)

	// Which is why the host checks the domain it pins as owner first.
	require.NotEqual(t, owner, first.Host.Domain)
}

// The signer's kind is checked before the document is read: a person key is
// refused as a person key, whatever bytes it signed.
func TestAPersonKeyIsRefusedAsSuchWhateverItSigned(t *testing.T) {
	person := verifierFor(t, pinFor(t, signerFor(t, testKeyID), solutionhost.KeyKindPerson, testDomain))
	canonical := envelopePayload(t, validEnvelope(t))
	manifest, err := solutionhost.SignedPayload(valid(t))
	require.NoError(t, err)
	for name, payload := range map[string][]byte{
		"canonical envelope":     canonical,
		"non-canonical envelope": append(append([]byte{}, canonical...), '\n'),
		"manifest":               manifest,
	} {
		signed, err := solutionhost.SignEnvelope(payload, signerFor(t, testKeyID))
		require.NoError(t, err)
		_, err = solutionhost.VerifyDeliveredEnvelope(context.Background(), &solutionhost.Signed{Document: payload, Envelope: signed}, person)
		require.ErrorIs(t, err, solutionhost.ErrEnvelopeSignerNotRelease, name)
	}
}

// The size bound holds before any byte is verified or decoded: an oversized
// carrier from an unpinned key is refused for its size, not its signature.
func TestADeliveredEnvelopeIsBoundedBeforeItIsVerified(t *testing.T) {
	huge := []byte(`{"schema":"` + solutionhost.SchemaModuleEnvelopeV1 + `","pad":"` + strings.Repeat("x", solutionhost.EnvelopeMaxBytes) + `"}`)
	_, err := solutionhost.VerifyDeliveredEnvelope(context.Background(), &solutionhost.Signed{Document: huge, Envelope: json.RawMessage(`{}`)}, testVerifier(t))
	require.ErrorIs(t, err, solutionhost.ErrEnvelopeInvalid)
	require.NotErrorIs(t, err, solutionhost.ErrUnsigned)

	carrier := signedWith(t, envelopePayload(t, validEnvelope(t)), signerFor(t, "stranger"))
	carrier.Envelope = json.RawMessage(`{"pad":"` + strings.Repeat("x", 2*solutionhost.EnvelopeMaxBytes) + `"}`)
	_, err = solutionhost.VerifyDeliveredEnvelope(context.Background(), carrier, testVerifier(t))
	require.ErrorIs(t, err, solutionhost.ErrEnvelopeInvalid)
}

// An envelope refusal keeps the shared sentinel for errors.Is, but never calls
// the document a module manifest — a host surfaces these words to a person.
func TestEnvelopeRefusalsNameTheEnvelope(t *testing.T) {
	ctx := context.Background()
	payload := envelopePayload(t, validEnvelope(t))
	type refusal struct{ err, want error }
	refusals := map[string]refusal{}
	var err error

	replaced := []byte(`{"schema":"codefly/solution-authority/v1"}`)
	_, err = solutionhost.EnvelopeFromVerified(replaced)
	refusals["replaced family"] = refusal{err, solutionhost.ErrSchema}
	_, err = solutionhost.VerifyDeliveredEnvelope(ctx, signedValid(t), testVerifier(t))
	refusals["manifest document"] = refusal{err, solutionhost.ErrSchema}
	_, err = solutionhost.VerifyDeliveredEnvelope(ctx, &solutionhost.Signed{Document: replaced, Envelope: json.RawMessage(`{}`)}, testVerifier(t))
	refusals["unknown document"] = refusal{err, solutionhost.ErrUnsigned}
	_, err = solutionhost.VerifyDeliveredEnvelope(ctx, &solutionhost.Signed{Document: payload}, testVerifier(t))
	refusals["no signature envelope"] = refusal{err, solutionhost.ErrUnsigned}
	_, err = solutionhost.VerifyDeliveredEnvelope(ctx, signedWith(t, payload, signerFor(t, "stranger")), testVerifier(t))
	refusals["untrusted"] = refusal{err, solutionhost.ErrUnsigned}
	_, err = solutionhost.EnvelopeFromVerified(append(append([]byte{}, payload...), ' '))
	refusals["not canonical"] = refusal{err, solutionhost.ErrNotCanonical}
	_, err = solutionhost.EnvelopeFromVerified(nil)
	refusals["empty"] = refusal{err, solutionhost.ErrUnsigned}
	otherDomain := verifierFor(t, pinFor(t, signerFor(t, testKeyID), solutionhost.KeyKindRelease, "other"))
	_, err = solutionhost.VerifyDeliveredEnvelope(ctx, signedWith(t, payload, signerFor(t, testKeyID)), otherDomain)
	refusals["wrong domain"] = refusal{err, solutionhost.ErrWrongDomain}
	elsewhere := validEnvelope(t)
	elsewhere.Host.Coordinate = "obin/local/other"
	_, err = solutionhost.AdmitEnvelope(testCoordinate, nil, elsewhere)
	refusals["wrong host"] = refusal{err, solutionhost.ErrWrongHost}
	unusable := validEnvelope(t)
	unusable.Revision = 0
	_, err = solutionhost.AdmitEnvelope(testCoordinate, unusable, validEnvelope(t))
	refusals["unusable previous"] = refusal{err, solutionhost.ErrAppliedUnusable}
	other := validEnvelope(t)
	other.Host.Domain = "other"
	_, err = solutionhost.AdmitEnvelope(testCoordinate, validEnvelope(t), other)
	refusals["domain change"] = refusal{err, solutionhost.ErrWrongDomain}

	for name, refused := range refusals {
		require.ErrorIs(t, refused.err, refused.want, name)
		require.NotContains(t, refused.err.Error(), "module manifest", name)
		require.Contains(t, refused.err.Error(), "module envelope", name)
	}
	require.NotContains(t, refusals["replaced family"].err.Error(), "replaced")
}

// Characters encoding/json escapes or repairs — HTML, the line separators,
// invalid UTF-8 — reach one canonical form and survive the carrier byte for
// byte. The raw spellings are not that form, so they are refused.
func TestAGrantsEscapedCharactersSurviveTheCarrier(t *testing.T) {
	envelope := validEnvelope(t)
	envelope.Principals["wiki"] = json.RawMessage("{\"html\":\"<a href='x'>&</a>\",\"separator\":\"\u2028\u2029\",\"broken\":\"\xff\"}")
	payload := envelopePayload(t, envelope)
	require.Contains(t, string(payload), `\u003ca href='x'\u003e\u0026\u003c/a\u003e`)
	require.Contains(t, string(payload), `\u2028\u2029`)
	require.Contains(t, string(payload), "\ufffd")

	written, err := solutionhost.MarshalSigned(signedWith(t, payload, signerFor(t, testKeyID)))
	require.NoError(t, err)
	read, err := solutionhost.ParseSigned(written)
	require.NoError(t, err)
	delivered, err := solutionhost.VerifyDeliveredEnvelope(context.Background(), read, testVerifier(t))
	require.NoError(t, err)
	require.Equal(t, payload, delivered.Payload())
	again, err := delivered.Envelope()
	require.NoError(t, err)
	digest, err := again.Digest()
	require.NoError(t, err)
	want, err := envelope.Digest()
	require.NoError(t, err)
	require.Equal(t, want, digest)

	raw := strings.Replace(string(payload), `\u003ca`, `<a`, 1)
	_, err = solutionhost.EnvelopeFromVerified([]byte(raw))
	require.ErrorIs(t, err, solutionhost.ErrNotCanonical)
}

// Whatever bytes arrive, an accepted envelope is a fixed point of its own
// canonical encoding, declares the envelope schema, and is never a manifest.
func FuzzEnvelopeFromVerified(f *testing.F) {
	envelope := &solutionhost.ModuleEnvelope{
		Schema: solutionhost.SchemaModuleEnvelopeV1, Host: solutionhost.HostTarget{Coordinate: testCoordinate, Domain: testDomain},
		Revision: 1, Principals: map[string]json.RawMessage{"wiki": json.RawMessage(`{"n":12345678901234567890,"s":"<\u2028>"}`)},
	}
	canonical, err := envelope.CanonicalBytes()
	if err != nil {
		f.Fatal(err)
	}
	f.Add(canonical)
	f.Add([]byte(`{"host":{"coordinate":"a","domain":"b"},"principals":{},"revision":1,"schema":"codefly/module-envelope/v1"}`))
	f.Add([]byte(`{"schema":"codefly/module-envelope/v1","principals":{"a":{"x":1,"x":2}}}`))
	f.Fuzz(func(t *testing.T, payload []byte) {
		read, err := solutionhost.EnvelopeFromVerified(payload)
		if err != nil {
			return
		}
		again, err := read.CanonicalBytes()
		if err != nil || string(again) != string(payload) {
			t.Fatalf("accepted %q, whose canonical encoding is %q (%v)", payload, again, err)
		}
		if schema := solutionhost.SchemaOf(payload); schema != solutionhost.SchemaModuleEnvelopeV1 {
			t.Fatalf("accepted %q declaring schema %q", payload, schema)
		}
		if _, err := solutionhost.ManifestFromVerified(payload); err == nil {
			t.Fatalf("%q is both an envelope and a manifest", payload)
		}
	})
}
