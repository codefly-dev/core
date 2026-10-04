package solutionhost_test

import (
	"context"
	"encoding/json"
	"errors"
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
// — the first-generation case, now STATED through FirstActivation rather than
// implied by zero values — so each test states only what it varies.
func activationOf(t *testing.T, a *solutionhost.AuthorityDocument, p *solutionhost.SolutionHostBinding, build solutionhost.ImageDigest) solutionhost.ActivationRequest {
	t.Helper()
	request := solutionhost.ActivationRequest{
		// The host asking. Activation bound to no host at all before this, so
		// both halves re-targeted elsewhere activated while Admit refused
		// them.
		Coordinate: fixtureHostCoordinate(t),
		Build:      build, Envelope: solutionhost.FixtureEnvelope(),
		DomainsBySigner: map[string][]string{fixtureDeliveredBy: {solutionhost.FixtureDomain, "beta"}},
		Domains:         []string{solutionhost.FixtureDomain, "beta"},
		// A host holding NO records for this binding, as host state core
		// READS rather than a marker the caller asserts.
		Records: recordsHolding(),
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
		// The Activation now says which host it is for, so it cannot be read
		// as an activation anywhere.
		Host: presenceDocument.Host,
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
	request.Records = recordsHolding(applied)
	_, err = solutionhost.Activate(request)
	require.ErrorIs(t, err, solutionhost.ErrStaleGeneration)

	// The current generation activates against its own applied record.
	request = activationOf(t, current, presenceDocument, build)
	request.Records = recordsHolding(applied)
	_, err = solutionhost.Activate(request)
	require.NoError(t, err)

	// A rewritten generation — same number, different content — is refused as
	// tampering rather than reapplied.
	rewritten := validAuthority(t)
	rewritten.EffectiveFrom = current.EffectiveFrom + 1
	request = activationOf(t, rewritten, presenceDocument, build)
	request.Records = recordsHolding(applied)
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
	request.Records = recordsHolding(withdrawn)
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
	envelope.Grants = []solutionhost.EnvelopeGrant{{Principal: "principal:operator", Binding: bare}}
	require.NoError(t, document.ValidateAgainst(envelope),
		"an absent queue matches an absent queue")

	// An envelope entry that HAS a queue does not grant the queueless binding,
	// and the queueless entry does not grant one that names a queue. Neither
	// direction is interchangeable, which is what stops absence being read as
	// a wildcard by whichever side reads it first.
	withQueue := bare
	withQueue.Queue = "reconcile.default"

	widened := solutionhost.FixtureEnvelope()
	widened.Grants = []solutionhost.EnvelopeGrant{{Principal: "principal:operator", Binding: withQueue}}
	require.ErrorIs(t, document.ValidateAgainst(widened), solutionhost.ErrOutsideEnvelope)

	claiming := validAuthority(t)
	claiming.Principals = []solutionhost.PrincipalAuthority{{
		Principal: "principal:operator",
		Bindings:  []solutionhost.AuthorityBinding{withQueue},
	}}
	narrow := solutionhost.FixtureEnvelope()
	narrow.Grants = []solutionhost.EnvelopeGrant{{Principal: "principal:operator", Binding: bare}}
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
	envelope.Grants = []solutionhost.EnvelopeGrant{{
		Principal: document.Principals[0].Principal,
		Binding:   document.Principals[0].Bindings[0],
	}}
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
// emptiness meant a renderer. What is wrong with that is simply that it makes
// a security check optional and indistinguishable from its absence — nothing
// in the call says whether the caller waived it or forgot the field.
//
// It was FIRST justified by a different argument, that no renderer can reach
// Activate because Delivered's fields are unexported. That argument is false:
// BundleVerifier is caller-supplied, so a permissive one yields a *Delivered
// over any bytes. TestADeliveredProvesOrderingAndNotIdentity holds that fact
// now, so the refuted claim cannot quietly come back as a comment.
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

	// THE PRESENCE HALF ON ITS OWN, which this test could not discriminate:
	// both halves carry the same fixture signer, so a policy that denied it
	// rejected the AUTHORITY half first and the presence branch was never
	// reached. Round six was right. Delivering the presence half under a
	// different signer, allowed for neither domain, is what isolates it.
	presenceUnderAnotherSigner, err := solutionhost.VerifyDelivered(
		context.Background(),
		carrierOf(t, presenceDocument),
		permissiveVerifier{as: "https://signer.example/other@refs/heads/main"},
	)
	require.NoError(t, err)
	onlyPresenceDenied := activationOf(t, authorityDocument, presenceDocument, build)
	onlyPresenceDenied.Presence = presenceUnderAnotherSigner
	_, err = solutionhost.Activate(onlyPresenceDenied)
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "the presence half",
		"the AUTHORITY half is allowed here, so only the presence branch can refuse this")

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
		Authority:            authorityDocument,
		Presence:             presenceDocument,
		Build:                build,
		EnvelopeRevision:     uint64(solutionhost.FixtureEnvelopeRevision),
		FirstAuthorityRecord: true, FirstPresenceRecord: true,
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
		Authority:            elsewhere,
		Presence:             presenceDocument,
		Build:                build,
		EnvelopeRevision:     uint64(solutionhost.FixtureEnvelopeRevision),
		FirstAuthorityRecord: true, FirstPresenceRecord: true,
	})
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "is granted over binding")

	// Each half alone still grants nothing.
	_, err = solutionhost.ActivateRendered(solutionhost.RenderedActivationRequest{
		Presence: presenceDocument, Build: build, EnvelopeRevision: uint64(solutionhost.FixtureEnvelopeRevision), FirstAuthorityRecord: true, FirstPresenceRecord: true,
	})
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	_, err = solutionhost.ActivateRendered(solutionhost.RenderedActivationRequest{
		Authority: authorityDocument, Build: build, EnvelopeRevision: uint64(solutionhost.FixtureEnvelopeRevision), FirstAuthorityRecord: true, FirstPresenceRecord: true,
	})
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
}

// A renderer can call ActivateRendered with what it actually holds: a revision
// number, not an envelope.
//
// It took an Envelope when it shipped, which made it uncallable. A renderer
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
		// A publish that read the base branch and found nothing says so
		// explicitly, rather than letting zero values mean it.
		FirstAuthorityRecord: true, FirstPresenceRecord: true,
	})
	require.NoError(t, err)
	require.Equal(t, revision, match.EnvelopeRevision)

	// Naming no revision is refused rather than treated as "no ceiling": a
	// tuple that agrees with itself about nothing activates nothing.
	_, err = solutionhost.ActivateRendered(solutionhost.RenderedActivationRequest{
		Authority: authorityDocument, Presence: presenceDocument, Build: build,
		FirstAuthorityRecord: true, FirstPresenceRecord: true,
	})
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "no envelope revision was named")

	// And both halves must name the revision the CALLER named. This is
	// stronger than the rule it replaced, which asked only that the two
	// halves agreed with EACH OTHER — a pair stamped against a superseded
	// ceiling satisfies that between themselves.
	_, err = solutionhost.ActivateRendered(solutionhost.RenderedActivationRequest{
		Authority: authorityDocument, Presence: presenceDocument, Build: build,
		EnvelopeRevision:     revision + 1,
		FirstAuthorityRecord: true, FirstPresenceRecord: true,
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

// A *Delivered proves ORDERING, not identity, and this test exists because I
// claimed otherwise in a comment and used the claim to justify a change.
//
// The claim was that a renderer cannot obtain the *Delivered halves Activate
// takes, Delivered's fields being unexported and VerifyDelivered its only
// constructor. A host consumer refuted it in nine lines, below:
// BundleVerifier is an interface the CALLER supplies, so a permissive
// implementation returning any identity produces a *Delivered with no
// attestation behind it. DeliveredBy's own comment already said so — the
// signer "is a string the CALLER handed it" — which is the part I had read and
// not connected.
//
// Requiring the policy was still right, for the plainer reason that an
// optional security check is indistinguishable from its absence. But the false
// argument was load-bearing prose: applied one file over it says
// Host.admit's `host.Coordinate != ""` guard is pointless, and
// TestTheCoordinateGuardIsLoadBearing shows what removing that costs.
func TestADeliveredProvesOrderingAndNotIdentity(t *testing.T) {
	document := valid(t)
	payload, err := document.CanonicalBytes()
	require.NoError(t, err)
	carrier, err := solutionhost.Carrier(payload, json.RawMessage(solutionhost.FixtureBundle))
	require.NoError(t, err)

	delivered, err := solutionhost.VerifyDelivered(context.Background(), carrier, permissiveVerifier{as: "whoever-i-say-i-am"})
	require.NoError(t, err)
	require.Equal(t, "whoever-i-say-i-am", delivered.DeliveredBy(),
		"a caller-supplied BundleVerifier decides the signer, so *Delivered carries no evidence about who signed")

	// What it DOES buy: those exact bytes passed through a verifier before any
	// judgement could read them, and the document re-derives from them rather
	// than from anything a caller held separately.
	rederived, err := delivered.Document()
	require.NoError(t, err)
	require.Equal(t, document.Binding, rederived.Binding)

	// And the host's policy is what turns a self-asserted signer into a
	// refusal, which is why it is required rather than optional.
	request := activationOf(t, validAuthority(t), document, document.Workloads[0].Image.Digest)
	request.Presence = delivered
	_, err = solutionhost.Activate(request)
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "does not let speak for domain")
}

// permissiveVerifier accepts anything and names whatever signer it is told to.
// It is that consumer's nine lines, kept as a test fixture because
// the claim it refutes was in a comment, a doc, a commit message and a PR
// comment.
type permissiveVerifier struct{ as string }

func (p permissiveVerifier) VerifyBundle(context.Context, []byte, json.RawMessage) (string, error) {
	return p.as, nil
}

// The `host.Coordinate != ""` guard on the DomainsBySigner requirement is
// LOAD-BEARING, and this test is the evidence that the argument I used against
// Activate's permissive branch must not be carried over to it.
//
// AdmitRendered routes through Host{}.admit — a zero Host, deliberately taking
// none — so the Coordinate guard is what lets a renderer reach the host-free
// checks at all. Requiring a signer policy unconditionally there would refuse
// every renderer.
func TestTheCoordinateGuardIsLoadBearing(t *testing.T) {
	// A renderer, with no host state of any kind, reaches the checks.
	admissions, err := solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: valid(t), FirstRecord: true})
	require.NoError(t, err)
	require.Len(t, admissions, 1)
	require.NoError(t, admissions[0].Err)

	// A NAMED host must still state the policy, which is the other side of the
	// same guard.
	host, err := solutionhost.FixtureHost()
	require.NoError(t, err)
	host.DomainsBySigner = nil
	_, err = solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: valid(t), FirstRecord: true})
	require.NoError(t, err, "a renderer is unaffected by host policy")
	_, err = host.Admit()
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "must declare which signer identities")
}

// TestActivationRunsThePresenceFoldToo is C3a: a tombstoned binding refused
// Admit of an older generation and ACTIVATED the same signed document.
//
// ActivationRequest carried an applied record for the AUTHORITY and none for
// the presence, so Activate consulted no presence state at all. Both halves
// are signed and both are replayable, so a fold on one of them is a fold on
// neither: the attacker presents the half that is not checked.
func TestActivationRunsThePresenceFoldToo(t *testing.T) {
	presenceDocument := valid(t)
	presenceDocument.Generation = 4
	authorityDocument := validAuthority(t)
	build := presenceDocument.Workloads[0].Image.Digest

	// A tombstone claims nothing at all.
	current := valid(t)
	current.Generation = 5
	current.Removed = true
	current.Routes, current.Artifacts, current.Workloads = nil, nil, nil
	current.Modules, current.Endpoints = nil, nil
	tombstone, err := solutionhost.AppliedFrom(current)
	require.NoError(t, err)

	request := activationOf(t, authorityDocument, presenceDocument, build)
	request.Records = recordsHolding(tombstone)

	_, err = solutionhost.Activate(request)
	require.Error(t, err, "a tombstoned binding activated an older signed presence")
	// The STALE sentinel, because generation 4 is behind the applied 5: that
	// is the correct reason for this input, and the point is that a presence
	// record is now consulted at all.
	require.ErrorIs(t, err, solutionhost.ErrStaleGeneration)

	// And the tombstone itself is terminal for a LATER generation, which is
	// the case the stale check cannot answer.
	later := valid(t)
	later.Generation = 6
	afterTombstone := activationOf(t, validAuthority(t), later, later.Workloads[0].Image.Digest)
	afterTombstone.Records = recordsHolding(tombstone)
	_, err = solutionhost.Activate(afterTombstone)
	require.ErrorIs(t, err, solutionhost.ErrTombstoned)
}

// TestAWithdrawnAuthorityCannotBeRenamedBackIntoLife is C3b: the same
// authority re-signed under a NEW ID activated a binding whose authority was
// withdrawn.
//
// The fold was keyed on the AUTHORITY ID, so a new ID had no applied record
// and the withdrawal did not apply to it — a tombstone defeated by renaming.
// Authority is granted over a binding, so the fold belongs on the binding, and
// the ID comparison now comes after the withdrawal check rather than before
// it. That ordering is the whole fix.
func TestAWithdrawnAuthorityCannotBeRenamedBackIntoLife(t *testing.T) {
	presenceDocument := valid(t)
	build := presenceDocument.Workloads[0].Image.Digest

	withdrawnDocument := validAuthority(t)
	withdrawnDocument.Removed = true
	withdrawnDocument.Generation = 3
	// A withdrawal claims nothing: no principals and no approved build.
	withdrawnDocument.Principals = nil
	withdrawnDocument.ApprovedBuild = ""
	withdrawnDocument.EffectiveFrom = 0
	withdrawn, err := solutionhost.AppliedAuthorityFrom(withdrawnDocument)
	require.NoError(t, err)

	// A NEW authority ID over the SAME binding, with the host holding only
	// the withdrawal of the old ID.
	renamed := validAuthority(t)
	renamed.Authority = withdrawnDocument.Authority + "-v2"
	renamed.Generation = 1
	require.Equal(t, withdrawnDocument.PresenceBinding, renamed.PresenceBinding)

	request := activationOf(t, renamed, presenceDocument, build)
	request.Records = recordsHolding(withdrawn)

	_, err = solutionhost.Activate(request)
	require.ErrorIs(t, err, solutionhost.ErrTombstoned)
	require.Contains(t, err.Error(), "terminal for the binding")
	require.Contains(t, err.Error(), "new authority ID does not reinstate it")
}

// TestCoreReadsTheAppliedStateItselfSoAbsenceCannotBeAsserted is R7-3, and it
// replaces a test of the marker it removed.
//
// The caller used to supply both records AND assert "there is none" per half.
// That was bypassable WITHOUT LYING: AppliedAuthority was documented "for one
// authority ID" while the fold must key on the binding, so a host that
// followed the documentation keyed its store by authority id, truthfully found
// no record for a renamed authority, truthfully set the marker, and activated
// a renamed authority over a WITHDRAWN binding. No mistake required, which is
// what made it worth an API change rather than a stronger warning.
//
// Core derives the binding from the attested presence bytes and reads both
// records under it. The caller chooses neither the key nor the answer.
func TestCoreReadsTheAppliedStateItselfSoAbsenceCannotBeAsserted(t *testing.T) {
	presenceDocument := valid(t)
	build := presenceDocument.Workloads[0].Image.Digest

	// A withdrawal of the OLD authority id, recorded under the binding.
	withdrawn := validAuthority(t)
	withdrawn.Removed = true
	withdrawn.Generation = 3
	withdrawn.Principals = nil
	withdrawn.ApprovedBuild = ""
	withdrawn.EffectiveFrom = 0
	record, err := solutionhost.AppliedAuthorityFrom(withdrawn)
	require.NoError(t, err)

	// The RENAMED authority, which a host keyed by authority id would find no
	// record for — and which used to activate.
	renamed := validAuthority(t)
	renamed.Authority = withdrawn.Authority + "-v2"
	renamed.Generation = 1

	request := activationOf(t, renamed, presenceDocument, build)
	request.Records = recordsHolding(record)
	_, err = solutionhost.Activate(request)
	require.ErrorIs(t, err, solutionhost.ErrTombstoned,
		"core keys the lookup on the binding, so the withdrawal reaches the renamed authority")
	require.Contains(t, err.Error(), "terminal for the binding")

	// A reader is REQUIRED: there is no way to say "assume nothing".
	noReader := activationOf(t, validAuthority(t), presenceDocument, build)
	noReader.Records = nil
	_, err = solutionhost.Activate(noReader)
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "needs a reader")

	// And a reader that CANNOT ANSWER refuses rather than reading as absent —
	// three outcomes, not two.
	_, err = solutionhost.Activate(func() solutionhost.ActivationRequest {
		r := activationOf(t, validAuthority(t), presenceDocument, build)
		r.Records = failingRecords{}
		return r
	}())
	require.ErrorIs(t, err, solutionhost.ErrAppliedUnusable)
}

// failingRecords cannot answer, which must not read as "holds nothing".
type failingRecords struct{}

func (failingRecords) AuthorityRecord(string) (solutionhost.AppliedAuthority, bool, error) {
	return solutionhost.AppliedAuthority{}, false, errors.New("store unavailable")
}

func (failingRecords) PresenceRecord(string) (solutionhost.Applied, bool, error) {
	return solutionhost.Applied{}, false, errors.New("store unavailable")
}

// TestAnUnnamedHostAdmitsNothing is C4: Host{}.Admit returned DecisionApply
// for a document from an unlisted signer, under an unlisted domain, targeting
// a foreign coordinate.
//
// Every provenance check in admit is guarded by `host.Coordinate != ""`,
// because the zero Host is how AdmitRendered reaches the host-free checks. But
// Host{} is constructible by anyone, so the public entrypoint handed back
// "apply" with every provenance rule skipped — and an attestation present,
// which makes it look checked.
func TestAnUnnamedHostAdmitsNothing(t *testing.T) {
	document := valid(t)
	document.OwnershipDomain = "not-a-listed-domain"
	document.Host.Coordinate = "some-other-host"

	_, err := solutionhost.Host{}.Admit(deliver(t, document))
	require.ErrorIs(t, err, solutionhost.ErrInvalid)
	require.Contains(t, err.Error(), "under its own coordinate")
	require.Contains(t, err.Error(), "AdmitRendered")

	// The zero-host path is still there and still correct: it is reached
	// through AdmitRendered, which takes no Host and so has nothing to forget.
	admissions, err := solutionhost.AdmitRenderedSets(solutionhost.RenderedSet{Document: valid(t), FirstRecord: true})
	require.NoError(t, err)
	require.Len(t, admissions, 1)
	require.NoError(t, admissions[0].Err)
}

// TestABindingCannotBeMovedBetweenPrincipals is C12: the envelope bounded the
// binding and not who may hold it.
//
// Envelope.Bindings was a bare []AuthorityBinding, so the ceiling said "this
// binding may exist" and never "held by whom". A document that moved
// binding:alpha:reconcile from the operator to any other principal passed
// containment unchanged — and the binding is exactly what a credential seals
// and a verifier looks up, so moving it is a grant of somebody else's
// authority. A ceiling that bounds half the statement bounds nothing.
func TestABindingCannotBeMovedBetweenPrincipals(t *testing.T) {
	document := validAuthority(t)
	envelope := solutionhost.FixtureEnvelope()
	require.NoError(t, document.ValidateAgainst(envelope), "the fixture pair is inside its own ceiling")

	moved := validAuthority(t)
	require.Equal(t, "principal:operator", moved.Principals[0].Principal)
	moved.Principals[0].Principal = "principal:attacker"

	err := moved.ValidateAgainst(envelope)
	require.ErrorIs(t, err, solutionhost.ErrOutsideEnvelope)
	require.Contains(t, err.Error(), "does not hold FOR THAT PRINCIPAL")
	require.Contains(t, err.Error(), "principal:attacker")

	// A clean MOVE between two principals the envelope does list, so this is a
	// pairing rule rather than a denylist of one unfamiliar name: reconcile
	// leaves the operator and arrives at the reporter, each ID still held
	// once.
	swapped := validAuthority(t)
	reconcile := swapped.Principals[0].Bindings[0]
	require.Equal(t, "binding:alpha:reconcile", reconcile.ID)
	swapped.Principals[0].Bindings = swapped.Principals[0].Bindings[1:]
	swapped.Principals[1].Bindings = append(swapped.Principals[1].Bindings, reconcile)
	require.NoError(t, swapped.Validate(), "each ID is still held exactly once")

	err = swapped.ValidateAgainst(envelope)
	require.ErrorIs(t, err, solutionhost.ErrOutsideEnvelope)
	require.Contains(t, err.Error(), "principal:reporter")
}

// fixtureHostCoordinate is the coordinate the fixture documents target, read
// from the fixture host rather than written twice.
func fixtureHostCoordinate(t *testing.T) string {
	t.Helper()
	host, err := solutionhost.FixtureHost()
	require.NoError(t, err)
	return host.Coordinate
}

// TestActivationBindsToTheHostThatAsks is R5.2/N4: activation bound to no host
// at all.
//
// ActivationRequest carried no coordinate, so both halves re-targeted to
// another host activated — while the SAME documents handed to Admit were
// refused with ErrWrongHost. A tuple written for one host activated on
// another: the provenance check admission has always made, and activation
// simply did not.
//
// The coordinate is named by the CALLER, for the reason the build is: reading
// the host out of the halves that claim it would make the question answer
// itself.
func TestActivationBindsToTheHostThatAsks(t *testing.T) {
	presenceDocument := valid(t)
	authorityDocument := validAuthority(t)
	build := presenceDocument.Workloads[0].Image.Digest

	// No coordinate at all is refused rather than read as "any host".
	bare := activationOf(t, authorityDocument, presenceDocument, build)
	bare.Coordinate = ""
	_, err := solutionhost.Activate(bare)
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "name its coordinate")

	// BOTH HALVES re-targeted elsewhere: Admit refuses these, and so must
	// activation.
	elsewhere := "example/prod/region-b"
	movedPresence := valid(t)
	movedPresence.Host.Coordinate = elsewhere
	movedAuthority := validAuthority(t)
	movedAuthority.Host.Coordinate = elsewhere

	_, err = fixtureHost(t).Admit(deliver(t, movedPresence))
	require.ErrorIs(t, err, solutionhost.ErrWrongHost,
		"this is the refusal admission has always made, and activation did not")

	moved := activationOf(t, movedAuthority, movedPresence, build)
	_, err = solutionhost.Activate(moved)
	require.ErrorIs(t, err, solutionhost.ErrWrongHost)
	require.Contains(t, err.Error(), elsewhere)

	// One half moved is refused too, naming which.
	halfMoved := activationOf(t, movedAuthority, presenceDocument, build)
	_, err = solutionhost.Activate(halfMoved)
	require.ErrorIs(t, err, solutionhost.ErrWrongHost)
	require.Contains(t, err.Error(), "authority "+movedAuthority.Authority)
}

// carrierOf assembles the signed carrier for a presence document, so a test
// can deliver the same document under a different attested signer.
func carrierOf(t *testing.T, document *solutionhost.SolutionHostBinding) *solutionhost.Signed {
	t.Helper()
	payload, err := document.CanonicalBytes()
	require.NoError(t, err)
	carrier, err := solutionhost.Carrier(payload, json.RawMessage(solutionhost.FixtureBundle))
	require.NoError(t, err)
	return carrier
}

// recordsHolding is an AppliedStateReader over whatever records a test hands
// it, keyed by binding exactly as a host's own store is. Passing none means
// the host holds none — which core now determines by LOOKING rather than by
// believing a marker.
func recordsHolding(records ...any) solutionhost.AppliedStateReader {
	held := testRecords{}
	for _, record := range records {
		switch typed := record.(type) {
		case solutionhost.AppliedAuthority:
			held.authority = append(held.authority, typed)
		case solutionhost.Applied:
			held.presence = append(held.presence, typed)
		}
	}
	return held
}

type testRecords struct {
	authority []solutionhost.AppliedAuthority
	presence  []solutionhost.Applied
}

func (r testRecords) AuthorityRecord(binding string) (solutionhost.AppliedAuthority, bool, error) {
	for _, record := range r.authority {
		if record.Binding == binding {
			return record, true, nil
		}
	}
	return solutionhost.AppliedAuthority{}, false, nil
}

func (r testRecords) PresenceRecord(binding string) (solutionhost.Applied, bool, error) {
	for _, record := range r.presence {
		if record.Binding == binding {
			return record, true, nil
		}
	}
	return solutionhost.Applied{}, false, nil
}

// TestActivationChecksTheDomainsTheHostAccepts is R7-4: activation checked WHO
// may speak for a domain and never whether the host accepts that domain at
// all, so an activation succeeded for a domain the host does not list while
// Admit refused the same documents.
//
// It is the hole Domains closes, one axis over from DomainsBySigner, and
// admission has always checked it.
func TestActivationChecksTheDomainsTheHostAccepts(t *testing.T) {
	presenceDocument := valid(t)
	authorityDocument := validAuthority(t)
	build := presenceDocument.Workloads[0].Image.Digest

	// The host accepts some OTHER domain. The signer policy still allows this
	// signer for this domain, so only the accepted-domains rule can refuse.
	notAccepted := activationOf(t, authorityDocument, presenceDocument, build)
	notAccepted.Domains = []string{"gamma"}
	_, err := solutionhost.Activate(notAccepted)
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain)
	require.Contains(t, err.Error(), "this host does not accept")

	// Admit refuses the same documents, which is the comparison that makes
	// this a missing check rather than a new rule.
	host := fixtureHost(t)
	host.Domains = []string{"gamma"}
	_, err = host.Admit(deliver(t, presenceDocument))
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain)

	// An unstated list is refused rather than read as "every domain".
	unstated := activationOf(t, authorityDocument, presenceDocument, build)
	unstated.Domains = nil
	_, err = solutionhost.Activate(unstated)
	require.ErrorIs(t, err, solutionhost.ErrNotActivated)
	require.Contains(t, err.Error(), "would accept every domain")
}

// Each reader method's failure is held separately, so neither error path can
// be deleted while the other covers for it.
func TestEitherAppliedReadFailingRefusesActivation(t *testing.T) {
	presenceDocument := valid(t)
	build := presenceDocument.Workloads[0].Image.Digest

	for name, records := range map[string]solutionhost.AppliedStateReader{
		"authority read fails": halfFailingRecords{authority: true},
		"presence read fails":  halfFailingRecords{},
	} {
		t.Run(name, func(t *testing.T) {
			request := activationOf(t, validAuthority(t), presenceDocument, build)
			request.Records = records
			_, err := solutionhost.Activate(request)
			require.ErrorIs(t, err, solutionhost.ErrAppliedUnusable,
				"a store that cannot answer must not read as a store holding nothing")
		})
	}
}

// halfFailingRecords fails exactly one of the two reads.
type halfFailingRecords struct{ authority bool }

func (r halfFailingRecords) AuthorityRecord(string) (solutionhost.AppliedAuthority, bool, error) {
	if r.authority {
		return solutionhost.AppliedAuthority{}, false, errors.New("authority store unavailable")
	}
	return solutionhost.AppliedAuthority{}, false, nil
}

func (r halfFailingRecords) PresenceRecord(string) (solutionhost.Applied, bool, error) {
	if r.authority {
		return solutionhost.Applied{}, false, nil
	}
	return solutionhost.Applied{}, false, errors.New("presence store unavailable")
}

// TestActivationDomainContinuityWithNoAuthorityRecord is F3's fifth guard and
// the moved-domain case R6-2 asked for and I did not commit: a full applied
// record under alpha, a document moved to beta, and NO authority record —
// which is the combination that reaches activation's own continuity check
// rather than the validator the rendered fold uses.
func TestActivationDomainContinuityWithNoAuthorityRecord(t *testing.T) {
	current := valid(t)
	applied, err := solutionhost.AppliedFrom(current)
	require.NoError(t, err)
	require.Equal(t, solutionhost.FixtureDomain, applied.Domain)

	moved := valid(t)
	moved.Generation = current.Generation + 1
	moved.OwnershipDomain = "beta"
	movedAuthority := validAuthority(t)
	movedAuthority.OwnershipDomain = "beta"

	request := activationOf(t, movedAuthority, moved, moved.Workloads[0].Image.Digest)
	// Only a PRESENCE record, under the old domain. No authority record.
	request.Records = recordsHolding(applied)
	_, err = solutionhost.Activate(request)
	require.ErrorIs(t, err, solutionhost.ErrWrongDomain,
		"the applied record's domain says who may change this binding, whatever the authority half holds")
	require.Contains(t, err.Error(), solutionhost.FixtureDomain)
	require.Contains(t, err.Error(), "beta")
}
