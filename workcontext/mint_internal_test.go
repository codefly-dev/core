package workcontext

import (
	"testing"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// Authority.seal refuses to sign a capability that carries no seal, or whose
// actor hop carries no epoch.
//
// This test is INSIDE the package because the guard it exercises is the last
// one before a token is handed out, and every external route to it fails
// earlier: the previous version of this test reached seal with a nil seal
// source, so it failed inside sealFor and would have stayed green with the
// guard deleted. A guard whose test passes without it is not a guard.
func TestSealRefusesToSignAnUnsealedCapability(t *testing.T) {
	authority := &Authority{Issuer: "https://issuer.test", KeyID: "k", Key: testSigningKey(t)}

	sound := func() *basev0.WorkContextV1 {
		return &basev0.WorkContextV1{
			Typ: Typ, Algorithm: Algorithm, KeyId: "k",
			Issuer: "https://issuer.test", Audience: "aud",
			Nonce: "n", ReplayPolicy: ReplayIdempotent,
			TenantId: "t", OwnerPrincipalId: "p", TaskId: "task", SessionId: "s",
			AuthorityScopes: []*basev0.WorkScopeV1{{ResourceKind: "record", Actions: []string{"read"}}},
			Seal: &basev0.WorkSealV1{
				PrincipalEpoch: 1, InstallationId: "inst",
				InstallationRevision: 1,
				BuildIncarnation:     fixtureIncarnationPointer(),
				ImageDigest:          fixtureDigestPointer(),
			},
		}
	}

	// The baseline signs, so the refusals below are about the thing removed
	// and not about the claims being unsound for some other reason.
	_, _, err := authority.seal(sound())
	require.NoError(t, err)

	// The SCHEMA refuses an unsealed capability now, so the minter's own
	// refusal is protovalidate's. The sentinel is ErrInvalid, and the
	// dedicated ErrUnsealed is deleted: a sentinel no branch can produce is
	// worse than none, because a consumer writes a handler that never runs.
	unsealed := sound()
	unsealed.Seal = nil
	_, _, err = authority.seal(unsealed)
	require.ErrorIs(t, err, ErrInvalid)

	// An actor hop with no epoch is a principal that could never be revoked,
	// and the minter will not hand one out either.
	hopless := sound()
	hopless.ActorChain = []*basev0.WorkActorV1{{
		PrincipalId: "actor", PrincipalKind: "service", DelegationId: "d",
		GrantedScopes: []*basev0.WorkScopeV1{{ResourceKind: "record", Actions: []string{"read"}}},
	}}
	_, _, err = authority.seal(hopless)
	require.ErrorIs(t, err, ErrInvalid)

	// With an epoch, the same hop signs.
	hopless.ActorChain[0].PrincipalEpoch = 1
	_, _, err = authority.seal(hopless)
	require.NoError(t, err)
}

func testSigningKey(t *testing.T) []byte {
	t.Helper()
	_, private := FixtureKeyPair()
	return private
}

// The execution fields carry explicit presence now, because a principal that
// bears no execution carries neither. These keep the literals above readable.
func fixtureIncarnationPointer() *uint64 {
	value := uint64(1)
	return &value
}

func fixtureDigestPointer() *string {
	value := FixtureImageDigest
	return &value
}

// The written-out fixture public key must stay equal to the key it names, or
// Verify's refusal silently stops matching anything.
//
// This lives INSIDE the package because the alternative was an exported
// IsFixtureKeyForTest hook, which round six rightly called a test hook in the
// public API — the same objection as the kit's other production-callable
// conveniences.
func TestFixturePublicKeyConstantMatchesTheDerivedKey(t *testing.T) {
	public, _ := FixtureKeyPair()
	require.True(t, isFixtureKey(public),
		"the written-out public key no longer matches FixtureKeyPair's")
	require.False(t, isFixtureKey(public[:len(public)-1]))
}
