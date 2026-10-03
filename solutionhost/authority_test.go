package solutionhost_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/codefly-dev/core/solutionhost"
	"github.com/stretchr/testify/require"
)

func validAuthority(t *testing.T) *solutionhost.AuthorityDocument {
	t.Helper()
	document, err := solutionhost.ParseAuthority(authority(t, "valid"))
	require.NoError(t, err)
	return document
}

func TestAuthorityDocumentCarriesEveryDeclaredField(t *testing.T) {
	document := validAuthority(t)

	require.Equal(t, solutionhost.SchemaAuthorityV1, document.Schema)
	require.Equal(t, "alpha-region-a-01-authority", document.Authority)
	require.Equal(t, uint64(2), document.Generation)
	require.Equal(t, solutionhost.FixtureCoordinate, document.Host.Coordinate)
	require.Equal(t, solutionhost.FixtureDomain, document.OwnershipDomain)
	require.Equal(t, uint64(solutionhost.FixtureEnvelopeRevision), document.EnvelopeRevision)
	require.Equal(t, uint64(4), document.EffectiveFrom)
	require.Regexp(t, `^sha256:[0-9a-f]{64}$`, string(document.ApprovedBuild))
	require.Len(t, document.Principals, 2)

	// Lookup is exact and by ID. There is deliberately no variant that searches
	// for a binding matching a set of scopes — that search is the thing the
	// sealed-ID credential contract exists to remove.
	binding, principal, held := document.Binding("binding:alpha:reconcile")
	require.True(t, held)
	require.Equal(t, "principal:operator", principal)
	require.Equal(t, uint64(3), binding.Revision)
	require.Equal(t, "reconcile", binding.Scope)

	_, _, held = document.Binding("binding:alpha:administer")
	require.False(t, held)
}

// activationOf builds a request with the current envelope and nothing applied
// — the first-generation case — so each test states only what it varies.
func activationOf(t *testing.T, a *solutionhost.AuthorityDocument, p *solutionhost.SolutionHostBinding, build solutionhost.ImageDigest) solutionhost.ActivationRequest {
	t.Helper()
	request := solutionhost.ActivationRequest{
		Build: build, Envelope: solutionhost.FixtureEnvelope(),
		DomainsBySigner: map[string][]string{fixtureDeliveredBy: {solutionhost.FixtureDomain, "beta"}},
	}
	if a != nil {
		request.Authority = deliverAuthority(t, a)
	}
	if p != nil {
		request.Presence = deliver(t, p)
	}
	return request
}

// deliverAuthority is deliver for the authority half: through the real
// VerifyDeliveredAuthority path, because that is the only way to obtain one.
func deliverAuthority(t *testing.T, document *solutionhost.AuthorityDocument) *solutionhost.DeliveredAuthority {
	t.Helper()
	payload, err := document.CanonicalBytes()
	require.NoError(t, err)
	carrier, err := solutionhost.Carrier(payload, json.RawMessage(solutionhost.FixtureBundle))
	require.NoError(t, err)
	delivered, err := solutionhost.VerifyDeliveredAuthority(context.Background(), carrier, testBundleVerifier{})
	require.NoError(t, err)
	return delivered
}

// An authority document grants nothing on its own, and neither does a presence
// document. Activation is a matched tuple, which is what makes "approved for
// one exact build" a property of the running system rather than of a field.
func TestActivationNeedsBothHalvesAndTheBuild(t *testing.T) {
	presenceDocument := valid(t)
	authorityDocument := validAuthority(t)
	build := presenceDocument.Workloads[0].Image.Digest

	activation, err := solutionhost.Activate(activationOf(t, authorityDocument, presenceDocument, build))
	require.NoError(t, err)
	require.Equal(t, solutionhost.Activation{
		Authority:        authorityDocument.Authority,
		Binding:          presenceDocument.Binding,
		Build:            build,
		Generation:       presenceDocument.Generation,
		EnvelopeRevision: uint64(solutionhost.FixtureEnvelopeRevision),
		Domain:           solutionhost.FixtureDomain,
	}, activation)

	_, err = solutionhost.Activate(activationOf(t, nil, presenceDocument, build))
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "presence alone grants nothing")

	_, err = solutionhost.Activate(activationOf(t, authorityDocument, nil, build))
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "authority alone grants nothing")
}

// The acceptance case: an authority document for build B is refused against a
// presence entry naming build A.
func TestAuthorityForAnotherBuildDoesNotActivate(t *testing.T) {
	presenceDocument := valid(t)
	otherBuild, err := solutionhost.ParseAuthority(authority(t, "other-build"))
	require.NoError(t, err)

	// The document is sound, and it is inside the envelope: that build is one
	// the envelope approved. What it is not is the build that is present.
	require.NoError(t, otherBuild.ValidateAgainst(solutionhost.FixtureEnvelope()))

	_, err = solutionhost.Activate(activationOf(t, otherBuild, presenceDocument, otherBuild.ApprovedBuild))
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "and not "+string(otherBuild.ApprovedBuild))

	// And asking about the build that IS present does not rescue it either: the
	// authority approves a different one.
	_, err = solutionhost.Activate(activationOf(t, otherBuild, presenceDocument, presenceDocument.Workloads[0].Image.Digest))
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "is approved for build")
}

// Every way the two halves can fail to line up, each refused.
//
// Each case asserts only that activation REFUSES, not which sentinel, because
// activation now checks the envelope first and some mismatches are caught
// there: an authority naming another envelope revision is outside the ceiling
// it claims, which ValidateAgainst answers before the two halves are compared.
// Refusing for the earlier reason is correct — the point is that nothing
// activates — and pinning a sentinel per case would pin the ORDER of the
// checks, which is not the contract.
func TestActivationRefusesEveryMismatch(t *testing.T) {
	for name, mutate := range map[string]func(*solutionhost.AuthorityDocument, *solutionhost.SolutionHostBinding){
		"another host coordinate": func(a *solutionhost.AuthorityDocument, _ *solutionhost.SolutionHostBinding) {
			a.Host.Coordinate = "example/prod/region-b"
		},
		"another host component": func(a *solutionhost.AuthorityDocument, _ *solutionhost.SolutionHostBinding) {
			a.Host.Component = "other-host"
		},
		"another ownership domain": func(a *solutionhost.AuthorityDocument, _ *solutionhost.SolutionHostBinding) {
			a.OwnershipDomain = "beta"
		},
		"another envelope revision": func(a *solutionhost.AuthorityDocument, _ *solutionhost.SolutionHostBinding) {
			a.EnvelopeRevision = solutionhost.FixtureEnvelopeRevision + 1
		},
		"effective from a later generation": func(a *solutionhost.AuthorityDocument, _ *solutionhost.SolutionHostBinding) {
			a.EffectiveFrom = 99
		},
		"withdrawn presence": func(_ *solutionhost.AuthorityDocument, p *solutionhost.SolutionHostBinding) {
			p.Removed = true
			p.Routes, p.Artifacts, p.Workloads, p.Modules, p.Endpoints = nil, nil, nil, nil, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			authorityDocument, presenceDocument := validAuthority(t), valid(t)
			build := presenceDocument.Workloads[0].Image.Digest
			mutate(authorityDocument, presenceDocument)

			_, err := solutionhost.Activate(activationOf(t, authorityDocument, presenceDocument, build))
			require.Error(t, err, "nothing may activate")
		})
	}
}

// An authority document is granted over ONE presence binding, and activates
// no other.
//
// Before AuthorityDocument.PresenceBinding existed, Activate matched on host,
// domain, envelope revision and build membership only — so an authority
// document activated ANY binding in the same host and domain running the same
// image, including a replacement instance that had taken a tombstoned
// binding's alias. Activation.Binding was copied from whichever presence
// document the caller passed in, so the result even reported the wrong target
// as if it had been checked.
func TestAuthorityActivatesOnlyTheBindingItNames(t *testing.T) {
	authorityDocument, presenceDocument := validAuthority(t), valid(t)
	build := presenceDocument.Workloads[0].Image.Digest
	require.Equal(t, presenceDocument.Binding, authorityDocument.PresenceBinding)

	// A different instance: same host, same domain, same image, new binding ID
	// — which is exactly what a replacement looks like.
	replacement := valid(t)
	replacement.Binding = "alpha-region-a-02"

	_, err := solutionhost.Activate(activationOf(t, authorityDocument, replacement, build))
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "is granted over binding")

	// And the one it does name still activates.
	_, err = solutionhost.Activate(activationOf(t, authorityDocument, presenceDocument, build))
	require.NoError(t, err)
}

// A signed authority document at an older generation activates nothing once a
// later one has been applied.
//
// This was the gap the field comment claimed was closed: "a replayed older
// document is detectable the same way a replayed presence generation is" —
// and nothing detected it. Keyless signatures do not expire, so a genuinely
// signed generation N-1 re-granted every binding generation N withdrew.
func TestAReplayedAuthorityGenerationActivatesNothing(t *testing.T) {
	current, presenceDocument := validAuthority(t), valid(t)
	build := presenceDocument.Workloads[0].Image.Digest
	applied, err := solutionhost.AppliedAuthorityFrom(current)
	require.NoError(t, err)

	// The same document, one generation back: sound, signed, and superseded.
	replayed := validAuthority(t)
	replayed.Generation = current.Generation - 1

	request := activationOf(t, replayed, presenceDocument, build)
	request.Applied = applied
	_, err = solutionhost.Activate(request)
	require.ErrorIs(t, err, solutionhost.ErrStaleGeneration)

	// The current generation activates against its own applied record.
	request = activationOf(t, current, presenceDocument, build)
	request.Applied = applied
	_, err = solutionhost.Activate(request)
	require.NoError(t, err)

	// A rewritten generation — same number, different content — is refused as
	// tampering rather than reapplied.
	rewritten := validAuthority(t)
	rewritten.EffectiveFrom = current.EffectiveFrom + 1
	request = activationOf(t, rewritten, presenceDocument, build)
	request.Applied = applied
	_, err = solutionhost.Activate(request)
	require.ErrorIs(t, err, solutionhost.ErrRewrittenGeneration)
}

// A withdrawn authority is TERMINAL: no later generation revives it, because
// everything keyed on the authority ID would re-attach.
func TestAWithdrawnAuthorityCannotBeRevived(t *testing.T) {
	presenceDocument := valid(t)
	build := presenceDocument.Workloads[0].Image.Digest
	tombstone, err := solutionhost.ParseAuthority(authority(t, "tombstone"))
	require.NoError(t, err)
	withdrawn, err := solutionhost.AppliedAuthorityFrom(tombstone)
	require.NoError(t, err)
	require.True(t, withdrawn.Removed)

	revival := validAuthority(t)
	revival.Generation = tombstone.Generation + 1
	request := activationOf(t, revival, presenceDocument, build)
	request.Applied = withdrawn
	_, err = solutionhost.Activate(request)
	require.ErrorIs(t, err, solutionhost.ErrTombstoned)
}

// Narrowing the envelope reaches activation, rather than relying on the caller
// having remembered ValidateAgainst first.
func TestActivationChecksTheEnvelopeItIsGiven(t *testing.T) {
	authorityDocument, presenceDocument := validAuthority(t), valid(t)
	build := presenceDocument.Workloads[0].Image.Digest

	request := activationOf(t, authorityDocument, presenceDocument, build)
	request.Envelope = solutionhost.Envelope{
		Revision:       solutionhost.FixtureEnvelopeRevision,
		ApprovedBuilds: []solutionhost.ImageDigest{build},
		// The ceiling now holds no bindings at all.
	}
	_, err := solutionhost.Activate(request)
	require.ErrorIs(t, err, solutionhost.ErrOutsideEnvelope)
}

// A withdrawn authority activates nothing, and withdrawal is a generation
// rather than an absence — so an unreadable mount can never be read as
// "withdraw every authority".
func TestWithdrawnAuthorityActivatesNothing(t *testing.T) {
	tombstone, err := solutionhost.ParseAuthority(authority(t, "tombstone"))
	require.NoError(t, err)
	require.True(t, tombstone.Removed)
	require.Empty(t, tombstone.Principals)
	require.Empty(t, tombstone.ApprovedBuild)
	require.Zero(t, tombstone.EffectiveFrom)

	presenceDocument := valid(t)
	_, err = solutionhost.Activate(activationOf(t, tombstone, presenceDocument, presenceDocument.Workloads[0].Image.Digest))
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "withdrawn")

	// A withdrawal that still named what it grants would be a half-removal a
	// host would have to pick a side on.
	tombstone.ApprovedBuild = presenceDocument.Workloads[0].Image.Digest
	require.ErrorIs(t, tombstone.Validate(), solutionhost.ErrInvalid)
}

// The build is passed in rather than read out of either document. Reading it
// out of the document that approves it would make the question answer itself.
func TestActivationRefusesABuildThatIsNotADigest(t *testing.T) {
	for _, build := range []solutionhost.ImageDigest{"", "latest", "sha256:short"} {
		_, err := solutionhost.Activate(activationOf(t, validAuthority(t), valid(t), build))
		require.ErrorIsf(t, err, solutionhost.ErrNotActivated, "build %q", build)
	}
}

// Containment is exact element inclusion, not subsumption. The fixture grants
// a plausible widening of a binding the envelope DOES hold, which is exactly
// how a subsumption rule would let it through.
func TestBindingsOutsideTheEnvelopeAreRefused(t *testing.T) {
	outside, err := solutionhost.ParseAuthority(authority(t, "outside-envelope"))
	require.NoError(t, err)

	err = outside.ValidateAgainst(solutionhost.FixtureEnvelope())
	require.ErrorIs(t, err, solutionhost.ErrOutsideEnvelope)
	require.Contains(t, err.Error(), "binding:alpha:administer")

	// Every field is part of the identity of a binding, so changing any one of
	// them puts the document outside a ceiling that holds the original.
	for name, mutate := range map[string]func(*solutionhost.AuthorityBinding){
		"another revision":  func(b *solutionhost.AuthorityBinding) { b.Revision = 99 },
		"another audience":  func(b *solutionhost.AuthorityBinding) { b.Audience = "https://elsewhere.example/operations" },
		"another scope":     func(b *solutionhost.AuthorityBinding) { b.Scope = "administer" },
		"another queue":     func(b *solutionhost.AuthorityBinding) { b.Queue = "reconcile.priority" },
		"another namespace": func(b *solutionhost.AuthorityBinding) { b.Namespace = "beta-region-a-01" },
		"another id":        func(b *solutionhost.AuthorityBinding) { b.ID = "binding:alpha:reconcile-2" },
	} {
		t.Run(name, func(t *testing.T) {
			document := validAuthority(t)
			mutate(&document.Principals[0].Bindings[0])
			require.ErrorIs(t, document.ValidateAgainst(solutionhost.FixtureEnvelope()), solutionhost.ErrOutsideEnvelope)
		})
	}
}

// A build the envelope has not approved is outside it, whatever the presence
// document runs.
func TestAnUnapprovedBuildIsOutsideTheEnvelope(t *testing.T) {
	document := validAuthority(t)
	document.ApprovedBuild = "sha256:1111111111111111111111111111111111111111111111111111111111111111"

	err := document.ValidateAgainst(solutionhost.FixtureEnvelope())
	require.ErrorIs(t, err, solutionhost.ErrOutsideEnvelope)
	require.Contains(t, err.Error(), "has not approved")
}

// Both halves carry the envelope revision and both must agree with the
// envelope in hand, so authority validated against a wider ceiling cannot
// activate presence validated against a narrower one.
func TestTheEnvelopeRevisionMustAgree(t *testing.T) {
	document := validAuthority(t)
	document.EnvelopeRevision = solutionhost.FixtureEnvelopeRevision + 1
	require.ErrorIs(t, document.ValidateAgainst(solutionhost.FixtureEnvelope()), solutionhost.ErrOutsideEnvelope)

	// An envelope with no revision bounds nothing: it cannot be told apart
	// from one nobody filled in.
	require.ErrorIs(t, validAuthority(t).ValidateAgainst(solutionhost.Envelope{}), solutionhost.ErrInvalid)
}

// A withdrawal claims nothing, so it is inside every envelope of its revision.
func TestAWithdrawalIsInsideEveryEnvelopeOfItsRevision(t *testing.T) {
	tombstone, err := solutionhost.ParseAuthority(authority(t, "tombstone"))
	require.NoError(t, err)
	require.NoError(t, tombstone.ValidateAgainst(solutionhost.FixtureEnvelope()))
	require.NoError(t, tombstone.ValidateAgainst(solutionhost.Envelope{Revision: solutionhost.FixtureEnvelopeRevision}))
}

func TestAuthorityValidationRejectsEachWayItCanLie(t *testing.T) {
	for name, mutate := range map[string]func(*solutionhost.AuthorityDocument){
		"no authority ID": func(d *solutionhost.AuthorityDocument) { d.Authority = "" },
		"authority ID with a space": func(d *solutionhost.AuthorityDocument) {
			d.Authority = "alpha authority"
		},
		"generation zero":        func(d *solutionhost.AuthorityDocument) { d.Generation = 0 },
		"no coordinate":          func(d *solutionhost.AuthorityDocument) { d.Host.Coordinate = "" },
		"no component":           func(d *solutionhost.AuthorityDocument) { d.Host.Component = "" },
		"no ownership domain":    func(d *solutionhost.AuthorityDocument) { d.OwnershipDomain = "" },
		"envelope revision zero": func(d *solutionhost.AuthorityDocument) { d.EnvelopeRevision = 0 },
		"no approved build":      func(d *solutionhost.AuthorityDocument) { d.ApprovedBuild = "" },
		"approved build is a tag": func(d *solutionhost.AuthorityDocument) {
			d.ApprovedBuild = "1.4.0"
		},
		"effective from zero": func(d *solutionhost.AuthorityDocument) { d.EffectiveFrom = 0 },
		"no principals":       func(d *solutionhost.AuthorityDocument) { d.Principals = nil },
		"principal declared twice": func(d *solutionhost.AuthorityDocument) {
			d.Principals = append(d.Principals, d.Principals[0])
		},
		"principal holding no binding": func(d *solutionhost.AuthorityDocument) {
			d.Principals[0].Bindings = nil
		},
		"no binding ID": func(d *solutionhost.AuthorityDocument) {
			d.Principals[0].Bindings[0].ID = ""
		},
		"binding revision zero": func(d *solutionhost.AuthorityDocument) {
			d.Principals[0].Bindings[0].Revision = 0
		},
		"binding with no scope": func(d *solutionhost.AuthorityDocument) {
			d.Principals[0].Bindings[0].Scope = ""
		},
		"binding with a multi-line queue": func(d *solutionhost.AuthorityDocument) {
			d.Principals[0].Bindings[0].Queue = "reconcile\ndefault"
		},
		"binding with a multi-line namespace": func(d *solutionhost.AuthorityDocument) {
			d.Principals[0].Bindings[0].Namespace = "alpha\tother"
		},
		"multi-line binding audience": func(d *solutionhost.AuthorityDocument) {
			d.Principals[0].Bindings[0].Audience = "-----BEGIN PRIVATE KEY-----\nMIIE\n-----END"
		},
		// One ID identifies one unit of authority. Two would make the exact
		// lookup the credential contract rests on ambiguous in the one place
		// that must never guess.
		"one binding ID for two principals": func(d *solutionhost.AuthorityDocument) {
			d.Principals[1].Bindings[0].ID = d.Principals[0].Bindings[0].ID
		},
	} {
		t.Run(name, func(t *testing.T) {
			document := validAuthority(t)
			mutate(document)
			require.Error(t, document.Validate())
		})
	}
}

func TestNilAuthorityDocumentValidates(t *testing.T) {
	var document *solutionhost.AuthorityDocument
	require.ErrorIs(t, document.Validate(), solutionhost.ErrInvalid)
	_, _, held := document.Binding("anything")
	require.False(t, held)
}

// An authority document must never carry its own ceiling, and strict decoding
// is what holds that: a document that tried would be refused for the unknown
// field rather than quietly having it ignored.
func TestAuthorityDocumentCannotCarryItsOwnEnvelope(t *testing.T) {
	_, err := solutionhost.ParseAuthority(append(authority(t, "valid"),
		[]byte("\napproved_builds:\n  - sha256:1111111111111111111111111111111111111111111111111111111111111111\n")...))
	require.Error(t, err)
	require.Contains(t, err.Error(), "approved_builds")
}

func TestAuthorityMarshalRoundTripsAndRefusesAnInvalidDocument(t *testing.T) {
	document := validAuthority(t)
	data, err := solutionhost.MarshalAuthority(document)
	require.NoError(t, err)

	reparsed, err := solutionhost.ParseAuthority(data)
	require.NoError(t, err)
	require.Equal(t, document, reparsed)

	document.Generation = 0
	_, err = solutionhost.MarshalAuthority(document)
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
}

// The authority document's canonical encoding has the same discipline as the
// presence document's, and for the same reason: it is the payload a signature
// covers, so it must not move when a struct field is reordered for
// readability.
func TestAuthorityCanonicalEncodingIgnoresDeclarationOrder(t *testing.T) {
	document := validAuthority(t)
	digest, err := document.Digest()
	require.NoError(t, err)
	require.Equal(t, authorityFixtureDigest, digest,
		"the canonical encoding moved; every signature over an authority document is now invalid")

	shuffled := validAuthority(t)
	shuffled.Principals = []solutionhost.PrincipalAuthority{document.Principals[1], document.Principals[0]}
	shuffled.Principals[1].Bindings = []solutionhost.AuthorityBinding{
		document.Principals[0].Bindings[1], document.Principals[0].Bindings[0],
	}
	shuffledDigest, err := shuffled.Digest()
	require.NoError(t, err)
	require.Equal(t, digest, shuffledDigest)

	changed := validAuthority(t)
	changed.Principals[0].Bindings[0].Revision = 4
	changedDigest, err := changed.Digest()
	require.NoError(t, err)
	require.NotEqual(t, digest, changedDigest)
}

// Pinned for the same reason the presence digests are: this encoding is what a
// signature covers, so moving it invalidates every signed authority document
// ever delivered.
const authorityFixtureDigest = "sha256:316846822e7a8b5703cd3ac87fe815d782bbf02e98355e7865bfda55c2f89b58"

// A module that owns no queue is a real case, so Queue and Namespace are
// optional. Absence means this binding grants NO authority on that dimension —
// never every queue — and what makes that safe is exact element inclusion: an
// absent queue matches only an absent queue in the envelope.
//
// Both directions are pinned here, because the whole safety of making the field
// optional rests on absence and presence not being interchangeable.
func TestAnAbsentQueueGrantsNothingRatherThanEverything(t *testing.T) {
	bare := solutionhost.AuthorityBinding{
		ID: "binding:alpha:queueless", Revision: 1,
		Audience: "https://prod.region-a.example/operations",
		Scope:    "record:read",
	}

	document := validAuthority(t)
	document.Principals = []solutionhost.PrincipalAuthority{{
		Principal: "principal:operator",
		Bindings:  []solutionhost.AuthorityBinding{bare},
	}}
	require.NoError(t, document.Validate(), "a binding with no queue and no namespace is a valid binding")

	envelope := solutionhost.FixtureEnvelope()
	envelope.Bindings = []solutionhost.AuthorityBinding{bare}
	require.NoError(t, document.ValidateAgainst(envelope),
		"an absent queue matches an absent queue")

	// An envelope entry that HAS a queue does not grant the queueless binding,
	// and the queueless entry does not grant one that names a queue. Neither
	// direction is interchangeable, which is what stops absence being read as
	// a wildcard by whichever side reads it first.
	withQueue := bare
	withQueue.Queue = "reconcile.default"

	widened := solutionhost.FixtureEnvelope()
	widened.Bindings = []solutionhost.AuthorityBinding{withQueue}
	require.ErrorIs(t, document.ValidateAgainst(widened), solutionhost.ErrOutsideEnvelope)

	claiming := validAuthority(t)
	claiming.Principals = []solutionhost.PrincipalAuthority{{
		Principal: "principal:operator",
		Bindings:  []solutionhost.AuthorityBinding{withQueue},
	}}
	narrow := solutionhost.FixtureEnvelope()
	narrow.Bindings = []solutionhost.AuthorityBinding{bare}
	require.ErrorIs(t, claiming.ValidateAgainst(narrow), solutionhost.ErrOutsideEnvelope)
}

// The ID shape the renderer derives — <principal>:<binding>:<operation>, with
// a comma-joined sorted scope — is admitted as given. Core compares IDs and
// never parses one, so a derived ID is delivery's business and stable across
// renders is better than random.
func TestTheRenderersDerivedBindingShapeIsAdmitted(t *testing.T) {
	document := validAuthority(t)
	document.Principals = []solutionhost.PrincipalAuthority{{
		Principal: "principal:consumer",
		Bindings: []solutionhost.AuthorityBinding{{
			ID:       "principal:consumer:records-binding:redact",
			Revision: 1,
			Audience: "https://prod.region-a.example/operations",
			Scope:    "record:read,record:write,summary:read",
		}},
	}}
	require.NoError(t, document.Validate())

	envelope := solutionhost.FixtureEnvelope()
	envelope.Bindings = document.Principals[0].Bindings
	require.NoError(t, document.ValidateAgainst(envelope))

	binding, principal, held := document.Binding("principal:consumer:records-binding:redact")
	require.True(t, held)
	require.Equal(t, "principal:consumer", principal)
	require.Equal(t, uint64(1), binding.Revision)
}

// TestActivationRequiresTheSignerPolicyAndHoldsItPerHalf is the test whose
// absence was the finding.
//
// Deleting the ENTIRE signer-policy branch from Activate left the whole
// repository suite green — not solutionhost's package, the whole `go test
// ./...`. Every test that passed DomainsBySigner passed a policy that
// ALLOWED the fixture signer, so none of them could tell the check from its
// absence. A security check no test holds is indistinguishable from one that
// was never written, and that is the more damning half of this finding.
//
// The hole itself was `if len(policy) > 0`, justified by a comment saying
// emptiness meant a renderer. No renderer can reach Activate: Delivered's
// fields are unexported and VerifyDelivered is its only constructor, so the
// permissive branch served exactly one caller — a host that forgot the field.
func TestActivationRequiresTheSignerPolicyAndHoldsItPerHalf(t *testing.T) {
	presenceDocument := valid(t)
	authorityDocument := validAuthority(t)
	build := presenceDocument.Workloads[0].Image.Digest

	// No policy: refused, where it used to activate.
	request := activationOf(t, authorityDocument, presenceDocument, build)
	request.DomainsBySigner = nil
	_, err := solutionhost.Activate(request)
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "needs the host's signer policy")
	require.Contains(t, err.Error(), "ActivateRendered")

	// A policy that names the signer but not THIS domain: refused, and the
	// message says which half and which domain. This is the assertion that
	// makes deleting the branch fail a test.
	request = activationOf(t, authorityDocument, presenceDocument, build)
	request.DomainsBySigner = map[string][]string{fixtureDeliveredBy: {"some-other-domain"}}
	_, err = solutionhost.Activate(request)
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "does not let speak for domain")
	require.Contains(t, err.Error(), solutionhost.FixtureDomain)

	// A policy naming a DIFFERENT signer: refused too, so the lookup is by
	// the attested signer and not merely non-empty.
	request = activationOf(t, authorityDocument, presenceDocument, build)
	request.DomainsBySigner = map[string][]string{
		"https://signer.example/other@refs/heads/main": {solutionhost.FixtureDomain},
	}
	_, err = solutionhost.Activate(request)
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "does not let speak for domain")
}

// A renderer holds parsed documents and no attestation, so it gets the match
// and a type that cannot be mistaken for an authorization conclusion.
func TestARendererGetsTheMatchAndNotAnActivation(t *testing.T) {
	presenceDocument := valid(t)
	authorityDocument := validAuthority(t)
	build := presenceDocument.Workloads[0].Image.Digest

	match, err := solutionhost.ActivateRendered(solutionhost.RenderedActivationRequest{
		Authority:        authorityDocument,
		Presence:         presenceDocument,
		Build:            build,
		EnvelopeRevision: uint64(solutionhost.FixtureEnvelopeRevision),
	})
	require.NoError(t, err)
	require.Equal(t, solutionhost.RenderedMatch{
		Authority:        authorityDocument.Authority,
		Binding:          presenceDocument.Binding,
		Build:            build,
		Generation:       presenceDocument.Generation,
		EnvelopeRevision: uint64(solutionhost.FixtureEnvelopeRevision),
		Domain:           solutionhost.FixtureDomain,
	}, match)

	// It runs the tuple rules, so a mismatched pair is refused here too — the
	// point of the entrypoint is that the renderer gets the real checks, not
	// a courtesy nil.
	elsewhere := validAuthority(t)
	elsewhere.PresenceBinding = "some-other-binding"
	_, err = solutionhost.ActivateRendered(solutionhost.RenderedActivationRequest{
		Authority:        elsewhere,
		Presence:         presenceDocument,
		Build:            build,
		EnvelopeRevision: uint64(solutionhost.FixtureEnvelopeRevision),
	})
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "is granted over binding")

	// Each half alone still grants nothing.
	_, err = solutionhost.ActivateRendered(solutionhost.RenderedActivationRequest{
		Presence: presenceDocument, Build: build, EnvelopeRevision: uint64(solutionhost.FixtureEnvelopeRevision),
	})
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	_, err = solutionhost.ActivateRendered(solutionhost.RenderedActivationRequest{
		Authority: authorityDocument, Build: build, EnvelopeRevision: uint64(solutionhost.FixtureEnvelopeRevision),
	})
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
}

// A renderer can call ActivateRendered with what it actually holds: a revision
// number, not an envelope.
//
// It took an Envelope when it shipped, which made it uncallable. cli#855
// reported why, and the report is the test: a renderer holds no envelope by
// design — the render derives an authority document from a module contract,
// which is a REQUEST, and the platform checks it against the ceiling at
// apply. A composition carries host.envelope_revision and nothing else of the
// envelope. So the only Envelope a renderer could pass is one assembled from
// the document under check, and ValidateAgainst tests that document's own
// ApprovedBuild against the envelope's approved list and its own bindings
// against the envelope's. The call would answer itself, which is the shape
// Envelope's own doc refuses: "an envelope a document carried would be a
// document declaring its own ceiling."
//
// A zero Envelope was no escape either — ValidateAgainst refuses it with "the
// envelope names no revision" — so there was no honest call at all. Shipping
// an entrypoint whose only possible caller must lie to it is the same defect
// as keeping a permissive branch for a caller who cannot exist, which is the
// finding ActivateRendered was added to fix.
func TestARendererActivatesWithARevisionAndNotAnEnvelope(t *testing.T) {
	presenceDocument := valid(t)
	authorityDocument := validAuthority(t)
	build := presenceDocument.Workloads[0].Image.Digest
	revision := uint64(solutionhost.FixtureEnvelopeRevision)

	// The honest call: no envelope anywhere in it.
	match, err := solutionhost.ActivateRendered(solutionhost.RenderedActivationRequest{
		Authority:        authorityDocument,
		Presence:         presenceDocument,
		Build:            build,
		EnvelopeRevision: revision,
		// Honest state for a publish that read the base branch, which is why
		// the field is not documented as "usually nothing".
		Applied: solutionhost.AppliedAuthority{},
	})
	require.NoError(t, err)
	require.Equal(t, revision, match.EnvelopeRevision)

	// Naming no revision is refused rather than treated as "no ceiling": a
	// tuple that agrees with itself about nothing activates nothing.
	_, err = solutionhost.ActivateRendered(solutionhost.RenderedActivationRequest{
		Authority: authorityDocument, Presence: presenceDocument, Build: build,
	})
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "no envelope revision was named")

	// And both halves must name the revision the CALLER named. This is
	// stronger than the rule it replaced, which asked only that the two
	// halves agreed with EACH OTHER — a pair stamped against a superseded
	// ceiling satisfies that between themselves.
	_, err = solutionhost.ActivateRendered(solutionhost.RenderedActivationRequest{
		Authority: authorityDocument, Presence: presenceDocument, Build: build,
		EnvelopeRevision: revision + 1,
	})
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "envelope revision")

	// The host entrypoint keeps the ceiling, because only a host can answer
	// it. Same documents, same build, and ValidateAgainst still runs there.
	request := activationOf(t, authorityDocument, presenceDocument, build)
	request.Envelope = solutionhost.Envelope{Revision: revision}
	_, err = solutionhost.Activate(request)
	require.ErrorIs(t, err, solutionhost.ErrOutsideEnvelope)
}
