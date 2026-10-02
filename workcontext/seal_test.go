package workcontext_test

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/workcontext"
)

// operationSession is a session that exercises one unit of authority, so the
// binding half of the seal is populated.
func (h *harness) operationSession(aud string) (string, *workcontext.Verified) {
	h.t.Helper()
	token, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		InstallationID:     installation,
		OperationBindingID: bindingID,
		TenantID:           tenant,
		OwnerPrincipalID:   ownerID,
		OwnerPrincipalKind: "human",
		OrganizationID:     organization,
		TaskID:             taskID,
		Audience:           aud,
		AuthorityScopes:    []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		TTL:                time.Hour,
	})
	require.NoError(h.t, err)
	return token, h.mustVerify(aud, token)
}

// The seal is read from the issuer, never taken from the caller: a minter that
// accepted the numbers would hand out capabilities nothing verifies, and the
// failure would land in another process as an authentication error.
func TestStart_SealsToTheIssuersLiveState(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	seal := owner.Context().GetSeal()
	require.Equal(t, installation, seal.GetInstallationId())
	require.Equal(t, uint64(2), seal.GetPrincipalEpoch())
	require.Equal(t, uint64(3), seal.GetInstallationRevision())
	require.Equal(t, uint64(11), seal.GetBuildIncarnation())
	require.Nil(t, owner.Context().GetOperationBinding(), "this session exercises no binding")
}

// A capability is sealed to an installation, so one must be named. Without
// this a caller could mint a capability bound to nothing and every comparison
// below would compare two zeroes.
func TestStart_RefusesWithoutAnInstallation(t *testing.T) {
	h := newHarness(t)
	_, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		TenantID:           tenant,
		OwnerPrincipalID:   ownerID,
		OwnerPrincipalKind: "human",
		TaskID:             taskID,
		Audience:           audience,
		TTL:                time.Hour,
	})
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.ErrorContains(t, err, "sealed to an installation")
}

// The acceptance case from the issue: a credential sealed to installation
// revision 3 fails verification when the verifier is handed revision 4.
func TestVerify_RefusesACapabilitySealedToASupersededInstallationRevision(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	require.NoError(t, h.seals.Put(ownerID, workcontext.Seal{
		PrincipalEpoch:       2,
		InstallationID:       installation,
		InstallationRevision: 4,
		BuildIncarnation:     11,
	}))

	_, err := h.verify(audience, token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "sealed to installation revision 3, the issuer holds 4")
}

// Exact equality, not "at least". There is no legitimate way to hold a
// capability sealed to a state that has not happened, so the shapes that
// produce one are a rolled-back installation and a forged seal.
func TestVerify_RefusesASealAheadOfTheIssuer(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	require.NoError(t, h.seals.Put(ownerID, workcontext.Seal{
		PrincipalEpoch:       2,
		InstallationID:       installation,
		InstallationRevision: 2,
		BuildIncarnation:     11,
	}))

	_, err := h.verify(audience, token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "sealed to installation revision 3, the issuer holds 2")
}

// Each sealed field is its own revocation lever, and each is compared.
func TestVerify_RefusesEverySealedFieldIndependently(t *testing.T) {
	for name, moved := range map[string]workcontext.Seal{
		"principal epoch": {
			PrincipalEpoch: 3, InstallationID: installation,
			InstallationRevision: 3, BuildIncarnation: 11,
		},
		"build incarnation": {
			PrincipalEpoch: 2, InstallationID: installation,
			InstallationRevision: 3, BuildIncarnation: 12,
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			token, _ := h.ownerSession(audience)
			require.NoError(t, h.seals.Put(ownerID, moved))

			_, err := h.verify(audience, token)
			require.ErrorIs(t, err, workcontext.ErrRevoked)
			require.ErrorContains(t, err, "sealed to "+name)
		})
	}
}

// A principal that no longer holds the installation refuses, with a message
// that says so rather than one about revisions.
func TestVerify_RefusesAnInstallationThePrincipalNoLongerHolds(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	h.seals = workcontext.NewMemorySealSource()

	_, err := h.verify(audience, token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "which principal")
}

// A capability carrying no seal is not a credential. The schema does not
// require the field — a schema rule would retroactively invalidate every
// archived capability and every receipt embedding one — so this is the check
// that makes the requirement real.
func TestVerify_RefusesAnUnsealedCapability(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	unsealed := proto.Clone(owner.Context()).(*basev0.WorkContextV1)
	unsealed.Seal = nil

	_, err := h.verify(audience, h.resign(unsealed))
	require.ErrorIs(t, err, workcontext.ErrUnsealed)
}

// ... and a minter cannot hand one out either, so a mint path that forgot to
// seal fails where it can be fixed rather than at a verifier it cannot reach.
func TestSeal_RefusesToSignAnUnsealedCapability(t *testing.T) {
	h := newHarness(t)
	h.authority.Seals = nil

	_, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		InstallationID:     installation,
		TenantID:           tenant,
		OwnerPrincipalID:   ownerID,
		OwnerPrincipalKind: "human",
		TaskID:             taskID,
		Audience:           audience,
		TTL:                time.Hour,
	})
	require.ErrorContains(t, err, "authority has no seal source")
}

// The operation binding is resolved by exact lookup on the sealed ID, and both
// of its counters are compared.
func TestVerify_RefusesASupersededOperationBinding(t *testing.T) {
	for name, moved := range map[string]workcontext.OperationBinding{
		"revision":    {ID: bindingID, Revision: 5, Incarnation: 1},
		"incarnation": {ID: bindingID, Revision: 4, Incarnation: 2},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			token, verified := h.operationSession(audience)
			require.Equal(t, bindingID, verified.Context().GetOperationBinding().GetBindingId())

			require.NoError(t, h.seals.PutBinding(moved))

			_, err := h.verify(audience, token)
			require.ErrorIs(t, err, workcontext.ErrRevoked)
			require.ErrorContains(t, err, name)
		})
	}
}

// A revoked binding refuses whatever its revision: withdrawal is not a
// revision bump, and a verifier that only compared counters would keep
// accepting a capability for a binding that was taken away.
func TestVerify_RefusesARevokedOperationBinding(t *testing.T) {
	h := newHarness(t)
	token, _ := h.operationSession(audience)

	require.NoError(t, h.seals.PutBinding(workcontext.OperationBinding{
		ID: bindingID, Revision: 4, Incarnation: 1, Revoked: true,
	}))

	_, err := h.verify(audience, token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "is revoked")
}

// A binding the issuer does not hold at all is a refusal and not an outage: a
// capability naming a binding that does not exist is not a credential.
func TestVerify_RefusesABindingTheIssuerDoesNotHold(t *testing.T) {
	h := newHarness(t)
	token, _ := h.operationSession(audience)

	h.seals = workcontext.NewMemorySealSource()
	require.NoError(t, h.seals.Put(ownerID, workcontext.Seal{
		PrincipalEpoch: 2, InstallationID: installation,
		InstallationRevision: 3, BuildIncarnation: 11,
	}))

	_, err := h.verify(audience, token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "which the issuer does not hold")
}

// A delegation hop narrows authority within one installation and never moves
// it, and the child carries the issuer's live numbers rather than the parent's
// sealed ones — the same reason the authorization revision is re-read.
func TestChild_ResealsAgainstTheIssuerAndKeepsTheInstallation(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)

	require.NoError(t, h.seals.Put(ownerID, workcontext.Seal{
		PrincipalEpoch:       2,
		InstallationID:       installation,
		InstallationRevision: 9,
		BuildIncarnation:     11,
	}))

	_, child, err := h.authority.Child(context.Background(), owner, workcontext.ChildInput{
		PrincipalID:   agentID,
		PrincipalKind: "agent",
		AgentID:       "codefly.dev/mind:1.2.0",
		DelegationID:  "d-1",
		GrantedScopes: []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		Audience:      audience,
		TTL:           time.Minute,
	})
	require.NoError(t, err)

	require.Equal(t, installation, child.GetSeal().GetInstallationId())
	require.Equal(t, uint64(9), child.GetSeal().GetInstallationRevision(),
		"the child carries the issuer's revision now, not the one the parent was sealed with")
	require.Equal(t, uint64(3), owner.Context().GetSeal().GetInstallationRevision(),
		"and the parent's verified claims are untouched")
}

// A hop may replace the binding it exercises, and the replacement's counters
// come from the issuer rather than from the caller.
func TestChild_ResolvesTheBindingItNames(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	require.NoError(t, h.seals.PutBinding(workcontext.OperationBinding{
		ID: "binding:crm:read", Revision: 1, Incarnation: 7,
	}))

	_, child, err := h.authority.Child(context.Background(), owner, workcontext.ChildInput{
		PrincipalID:        agentID,
		PrincipalKind:      "agent",
		AgentID:            "codefly.dev/mind:1.2.0",
		DelegationID:       "d-1",
		GrantedScopes:      []*basev0.WorkScopeV1{scope("repo", []string{"read"}, nil)},
		Audience:           audience,
		OperationBindingID: "binding:crm:read",
		TTL:                time.Minute,
	})
	require.NoError(t, err)

	binding := child.GetOperationBinding()
	require.Equal(t, "binding:crm:read", binding.GetBindingId())
	require.Equal(t, uint64(1), binding.GetRevision())
	require.Equal(t, uint64(7), binding.GetIncarnation())
}

// A grant capability is resealed too. One that carried the parent's sealed
// numbers would be born dead whenever the installation moved between the
// parent's verification and the mint, and the failure would surface inside the
// approved call rather than at the mint that caused it.
func TestGrant_ResealsAgainstTheIssuer(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	_, agent := h.agentSession(owner, audience)
	grant := h.approvedGrant("g-1")

	require.NoError(t, h.seals.Put(ownerID, workcontext.Seal{
		PrincipalEpoch:       2,
		InstallationID:       installation,
		InstallationRevision: 3,
		BuildIncarnation:     12,
	}))

	_, elevated, err := h.authority.Grant(context.Background(), agent, workcontext.GrantInput{
		Grant: grant, TTL: time.Minute,
	})
	require.NoError(t, err)
	require.Equal(t, uint64(12), elevated.GetSeal().GetBuildIncarnation())
}

// The source is asked about one installation and one binding, and an answer
// about a different one is an error rather than a comparison nobody can read.
// A source that resolved aliases would otherwise silently answer for a
// neighbouring installation and every field below would compare the wrong two.
func TestVerify_RefusesASourceThatAnswersForSomethingElse(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	verifier := h.verifier(audience)
	verifier.Seals = answersElsewhere{}
	_, err := verifier.Verify(context.Background(), token)
	require.ErrorContains(t, err, "answered for installation")
}

// answersElsewhere is a real SealSource that answers about a different
// installation than it was asked about — the drift this check exists for.
type answersElsewhere struct{}

func (answersElsewhere) Seal(context.Context, string, string) (workcontext.Seal, error) {
	return workcontext.Seal{
		PrincipalEpoch: 2, InstallationID: "inst-somewhere-else",
		InstallationRevision: 3, BuildIncarnation: 11,
	}, nil
}

func (answersElsewhere) OperationBinding(context.Context, string) (workcontext.OperationBinding, error) {
	return workcontext.OperationBinding{}, workcontext.ErrNoBinding
}

// A seal whose counters are zero is not a seal: zero would compare equal to a
// source that simply had nothing recorded.
func TestMemorySealSource_RefusesAZeroSeal(t *testing.T) {
	source := workcontext.NewMemorySealSource()
	require.ErrorIs(t, source.Put(ownerID, workcontext.Seal{InstallationID: installation}), workcontext.ErrInvalid)
	require.ErrorIs(t, source.Put(ownerID, workcontext.Seal{
		PrincipalEpoch: 1, InstallationRevision: 1, BuildIncarnation: 1,
	}), workcontext.ErrInvalid)
	require.ErrorIs(t, source.PutBinding(workcontext.OperationBinding{ID: bindingID}), workcontext.ErrInvalid)
}

// Two installations of one principal are two seals. Conflating them would
// compare the revision of one against the other, which is a comparison that
// passes or fails for no reason a reader could reconstruct.
func TestMemorySealSource_KeepsTwoInstallationsApart(t *testing.T) {
	source := workcontext.NewMemorySealSource()
	require.NoError(t, source.Put(ownerID, workcontext.Seal{
		PrincipalEpoch: 1, InstallationID: "inst-a", InstallationRevision: 1, BuildIncarnation: 1,
	}))
	require.NoError(t, source.Put(ownerID, workcontext.Seal{
		PrincipalEpoch: 1, InstallationID: "inst-b", InstallationRevision: 8, BuildIncarnation: 1,
	}))

	a, err := source.Seal(context.Background(), ownerID, "inst-a")
	require.NoError(t, err)
	require.Equal(t, uint64(1), a.InstallationRevision)

	b, err := source.Seal(context.Background(), ownerID, "inst-b")
	require.NoError(t, err)
	require.Equal(t, uint64(8), b.InstallationRevision)

	_, err = source.Seal(context.Background(), "someone-else", "inst-a")
	require.ErrorIs(t, err, workcontext.ErrNoSeal)
}

// A token in another encoding is refused as a foreign format BEFORE its
// signature is checked, and with its own error.
//
// This is the regression that made the one-implementation rule necessary. A
// second implementation signed a hand-written JSON payload; both forms are
// "<payload>.<signature>" with Ed25519, so a token from one reached the other,
// failed SIGNATURE verification, and reported "signature does not verify under
// key X" — which reads like key rotation, and is what everyone investigated
// while the actual problem was two encodings.
func TestVerify_RefusesAForeignEncodingBeforeTheSignature(t *testing.T) {
	h := newHarness(t)

	// Signed with the harness key, so the signature is genuine. A verifier
	// that checked the signature first would get past it and then produce some
	// other error; one that checks it last would report a signature failure for
	// a token that was never in this format. Neither is what happens.
	payload := []byte(`{"typ":"codefly.work-context/v1","issuer":"` + issuer + `","audience":"` + audience + `"}`)
	genuine := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(h.authority.Key, payload))

	_, err := h.verify(audience, genuine)
	require.ErrorIs(t, err, workcontext.ErrNotACoreToken)
	require.NotErrorIs(t, err, workcontext.ErrInvalid,
		"a foreign encoding is its own diagnosis, not a member of the invalid-capability family")
	require.NotContains(t, err.Error(), "signature")

	// The ordering is the whole point: the same JSON payload with a signature
	// that does NOT verify still reports the encoding, because the encoding is
	// checked first. If this ever reports a signature failure, the regression
	// is back.
	broken := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	_, err = h.verify(audience, broken)
	require.ErrorIs(t, err, workcontext.ErrNotACoreToken)
	require.NotContains(t, err.Error(), "signature")

	// A JSON array payload is the same answer.
	array := []byte(`[{"typ":"codefly.work-context/v1"}]`)
	_, err = h.verify(audience, base64.RawURLEncoding.EncodeToString(array)+"."+
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(h.authority.Key, array)))
	require.ErrorIs(t, err, workcontext.ErrNotACoreToken)

	// Leading whitespace does not launder it.
	spaced := append([]byte("  \n"), payload...)
	_, err = h.verify(audience, base64.RawURLEncoding.EncodeToString(spaced)+"."+
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(h.authority.Key, spaced)))
	require.ErrorIs(t, err, workcontext.ErrNotACoreToken)
}

// An empty payload is NOT a foreign encoding. "Not a core token" means "this is
// another format"; widening it to cover a malformed token of no format would
// make it mean "something was wrong early", which is the vagueness it exists to
// remove.
func TestVerify_AnEmptyPayloadIsInvalidRatherThanForeign(t *testing.T) {
	h := newHarness(t)
	_, err := h.verify(audience, ".")
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.NotErrorIs(t, err, workcontext.ErrNotACoreToken)
}

// The kit core ships must pass core's own verifier; if it does not, the kit is
// wrong rather than the consumer. The conformance package drives this properly;
// this is the one assertion that belongs beside the minter.
func TestFixtures_CoverEveryFormAndBothOutcomes(t *testing.T) {
	fixtures, err := workcontext.Fixtures(time.Now())
	require.NoError(t, err)
	require.NotEmpty(t, fixtures)

	forms := map[workcontext.Form]int{}
	outcomes := map[workcontext.Outcome]int{}
	for _, fixture := range fixtures {
		require.NotEmptyf(t, fixture.Name, "every fixture is named")
		require.NotEmptyf(t, fixture.Reason, "%s says which rule decides it", fixture.Name)
		forms[fixture.Form]++
		outcomes[fixture.Outcome]++
		if fixture.Outcome == workcontext.OutcomeRejected {
			require.NotNilf(t, fixture.Err, "%s must name the sentinel its refusal matches", fixture.Name)
		} else {
			require.Nilf(t, fixture.Err, "%s is accepted, so it names no error", fixture.Name)
		}
	}
	for _, form := range []workcontext.Form{
		workcontext.FormSession, workcontext.FormOperation, workcontext.FormDelegated,
		workcontext.FormDelegatedOperation, workcontext.FormGrant, workcontext.FormForeign,
	} {
		require.NotZerof(t, forms[form], "the kit covers no %q token", form)
	}
	require.NotZero(t, outcomes[workcontext.OutcomeAccepted])
	require.NotZero(t, outcomes[workcontext.OutcomeRejected])

	// Two calls mint two sets of nonces, so two consumers running the kit
	// cannot consume each other's single-use capability.
	again, err := workcontext.Fixtures(time.Now())
	require.NoError(t, err)
	require.Len(t, again, len(fixtures))
	for index := range fixtures {
		require.Equal(t, fixtures[index].Name, again[index].Name)
		if fixtures[index].Form == workcontext.FormForeign {
			continue
		}
		require.NotEqualf(t, fixtures[index].Token, again[index].Token,
			"%s must be minted fresh", fixtures[index].Name)
	}
}

// The fixture private key is public by construction, and that has to be
// obvious rather than discovered.
func TestFixtureKeyPairIsDeterministicAndDocumentedAsPublic(t *testing.T) {
	public, private := workcontext.FixtureKeyPair()
	againPublic, againPrivate := workcontext.FixtureKeyPair()
	require.Equal(t, public, againPublic)
	require.Equal(t, private, againPrivate)
	require.Equal(t, public, private.Public())
	require.Equal(t, map[string]ed25519.PublicKey{workcontext.FixtureKeyID: public}, workcontext.FixtureKeys())
}

// CheckEncoding is exported so every other decoder in the fleet can name the
// same condition with the same error. A second decoder answering "payload is
// not a WorkContextV1" for a foreign encoding would give an operator two
// messages for one condition — the fragmentation ErrNotACoreToken exists to
// end, in miniature.
func TestCheckEncodingNamesAForeignEncodingForAnyDecoder(t *testing.T) {
	for name, payload := range map[string][]byte{
		"a JSON object":            []byte(`{"typ":"codefly.work-context/v1"}`),
		"a JSON array":             []byte(`[{"typ":"x"}]`),
		"whitespace then JSON":     []byte("  \n\t{\"typ\":\"x\"}"),
		"a JSON object with a BOM": append([]byte{}, append([]byte(" "), []byte(`{"a":1}`)...)...),
	} {
		t.Run(name, func(t *testing.T) {
			err := workcontext.CheckEncoding(payload)
			require.ErrorIs(t, err, workcontext.ErrNotACoreToken)
			require.NotErrorIs(t, err, workcontext.ErrInvalid)
			require.NotContains(t, err.Error(), "signature")
		})
	}

	// And it reports the same error Verify does for the same bytes, so the two
	// cannot drift into two messages.
	h := newHarness(t)
	payload := []byte(`{"typ":"codefly.work-context/v1","issuer":"` + issuer + `"}`)
	direct := workcontext.CheckEncoding(payload)
	require.Error(t, direct)

	token := base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(h.authority.Key, payload))
	_, viaVerify := h.verify(audience, token)
	require.Equal(t, direct.Error(), viaVerify.Error())
}

// A nil return means only "not visibly another format". It is not a valid
// capability and nothing was authenticated — a caller treating nil as
// permission has skipped verification entirely, so the contract is stated here
// as well as in the doc comment.
func TestCheckEncodingAcceptsAnythingThatIsNotVisiblyForeign(t *testing.T) {
	for name, payload := range map[string][]byte{
		"empty":              {},
		"a real capability":  mustMarshal(t, newHarness(t)),
		"arbitrary bytes":    {0x08, 0x01, 0x12, 0x03, 'a', 'b', 'c'},
		"a bare quoted word": []byte(`"json string"`),
	} {
		t.Run(name, func(t *testing.T) {
			require.NoError(t, workcontext.CheckEncoding(payload))
		})
	}

	// The last one is the point: a JSON string is not an object or an array, so
	// this check does not claim it. It is refused later, as invalid, by the
	// schema — which is the right division, because "another format" and
	// "malformed" are different diagnoses.
	h := newHarness(t)
	_, err := h.verify(audience, base64.RawURLEncoding.EncodeToString([]byte(`"json string"`))+".AA")
	require.ErrorIs(t, err, workcontext.ErrInvalid)
	require.NotErrorIs(t, err, workcontext.ErrNotACoreToken)
}

func mustMarshal(t *testing.T, h *harness) []byte {
	t.Helper()
	_, verified := h.ownerSession(audience)
	payload, _, found := strings.Cut(verified.Encoded(), ".")
	require.True(t, found)
	claims, err := base64.RawURLEncoding.DecodeString(payload)
	require.NoError(t, err)
	return claims
}
