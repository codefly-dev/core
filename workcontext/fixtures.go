package workcontext

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
)

// The conformance kit: every token form this package mints, every way one is
// refused, as CODE rather than as prose.
//
// It exists because a wire contract with one implementation is a claim, and a
// claim about code is worth what a test says about it. A consumer runs
// workcontext/conformance.Run against its own verification entrypoint; if that
// entrypoint is this package's, every outcome below is reached, and if it is a
// second implementation, it is not. In particular a second implementation
// cannot pass the look-alike fixture, because refusing a foreign encoding
// BEFORE the signature check with its own error is the one behaviour a
// re-implementation never thinks to copy — it is the behaviour that exists
// because the re-implementation happened.
//
// The tokens are minted fresh on each call rather than committed as bytes. A
// capability carries a validity window, so committed bytes would expire and the
// kit would rot into a test that fails for the wrong reason once a year. What is
// fixed is everything a verifier is configured with: the keypair, the issuer,
// the audience, the identities, and every sealed value. Those are the constants
// below, and a consumer wires its verifier from them.

// The fixture keypair. It is derived from a fixed seed so a consumer can hold
// the public key as a constant, which means THE PRIVATE KEY IS PUBLIC — it is
// derivable from this source file by anyone. It signs conformance tokens and
// nothing else, ever.
const fixtureSeed = "codefly work context conformance fixtures"

// Fixture identities and sealed values. A consumer configures its verifier and
// its seal source with exactly these, or the kit tells it nothing.
const (
	// FixtureIssuer is the authority a conforming verifier must pin.
	FixtureIssuer = "https://authority.codefly.test"
	// FixtureAudience is the service the accepted fixtures are minted for.
	FixtureAudience = "codefly.test/conformance"
	// FixtureKeyID names the fixture verification key.
	FixtureKeyID = "conformance-1"
	// FixtureTenant is the tenant every fixture runs in.
	FixtureTenant = "tenant-conformance"
	// FixturePrincipal is the owner on whose authority every fixture runs.
	FixturePrincipal = "principal-owner"
	// FixtureActor is the delegated actor of the delegated fixtures.
	FixtureActor = "principal-actor"
	// FixtureAgentID is FixtureActor's agent manifest identity.
	FixtureAgentID = "codefly.test/actor:1.0.0"
	// FixtureApprover is the principal whose decision produces the grant.
	FixtureApprover = "principal-approver"
	// FixtureTask is the task every fixture belongs to.
	FixtureTask = "task-conformance"
	// FixtureOrganization is the organization every fixture runs in.
	FixtureOrganization = "organization-conformance"

	// FixtureInstallation is the installation every fixture is sealed to.
	FixtureInstallation = "installation-conformance"
	// FixturePrincipalEpoch is the live principal epoch.
	FixturePrincipalEpoch = 2
	// FixtureInstallationRevision is the live installation revision.
	FixtureInstallationRevision = 3
	// FixtureBuildIncarnation is the live build incarnation.
	FixtureBuildIncarnation = 11
	// FixtureImageDigest is the approved build for the fixture installation.
	FixtureImageDigest = "sha256:eca6c756839cbd532a6c7cb16fa75263600f3c710738ac267fb3988e03e146aa"

	// FixtureOtherImageDigest is a well-formed digest the fixture issuer does
	// NOT approve, so the kit can present a capability sealed to an unapproved
	// build AT THE LIVE INCARNATION — the shape a verifier comparing only
	// counters accepts.
	FixtureOtherImageDigest = "sha256:1dc9f4d2af4bb3d4a8c5d1f2e0b7a6c3d8e5f2a9b6c3d0e7f4a1b8c5d2e9f6a3"
	// FixtureActorEpoch is FixtureActor's live epoch, distinct from the
	// owner's so a fixture that advances one does not move the other.
	FixtureActorEpoch = 5

	// FixtureBindingID is the operation binding the operation fixtures seal.
	FixtureBindingID = "binding-conformance"
	// FixtureBindingRevision is that binding's live revision.
	FixtureBindingRevision = 4
	// FixtureBindingIncarnation is that binding's live incarnation.
	FixtureBindingIncarnation = 1
	// FixtureActorBindingID is the binding granted to FixtureActor, which the
	// delegated-operation fixture exercises.
	FixtureActorBindingID = "binding-conformance-actor"
	// FixtureOtherBindingID is a binding the issuer holds and grants to
	// another principal, so "sealed to a binding the caller does not hold" is
	// distinguishable from "sealed to a binding that does not exist".
	FixtureOtherBindingID = "binding-conformance-other"
	// FixtureMovedRevisionBindingID is granted to the SAME principal in the
	// SAME installation, at a revision the live source has moved past. It
	// exists so the revision half of the binding check is testable on its
	// own.
	//
	// The wrong-binding-revision fixture used FixtureOtherBindingID, which the
	// live source grants to ANOTHER principal — so that fixture changed
	// entitlement as well as revision, and since entitlement is checked first
	// the refusal was the entitlement while the comment claimed the revision.
	// Two variables, one asserted outcome.
	FixtureMovedRevisionBindingID = "binding-conformance-moved-revision"
	// FixtureForeignInstallationBindingID is granted within another
	// installation, so the installation half of the association is testable
	// separately from the principal half.
	FixtureForeignInstallationBindingID = "binding-conformance-foreign-installation"
	// FixtureRevokedBindingID is a binding the issuer has WITHDRAWN. It is a
	// binding of its own rather than a flag on another, so a capability can be
	// minted sealed to it while it was still live.
	FixtureRevokedBindingID = "binding-conformance-revoked"
	// FixtureReincarnatedBindingID was withdrawn and re-created, so the issuer
	// holds it at a higher incarnation than a capability sealed to the old one
	// carries, at the SAME revision.
	FixtureReincarnatedBindingID = "binding-conformance-reincarnated"

	// FixtureAuthorizationRevision is the issuer's live authorization revision.
	FixtureAuthorizationRevision = 7
	// FixtureGrantID is the approval the grant fixture carries.
	FixtureGrantID = "grant-conformance"
	// FixtureGrantSubject is what that grant is pinned to.
	FixtureGrantSubject = "resource-conformance"
	// FixtureGrantRequestDigest is the call that grant was approved for.
	FixtureGrantRequestDigest = "sha256:conformance"
)

// Form is which shape of capability a fixture is. Every form this package mints
// has one, so a consumer cannot pass the kit while handling only sessions.
type Form string

const (
	// FormSession is the first session of a task: the owner acting directly,
	// with no actor hop.
	FormSession Form = "session"
	// FormOperation is a session that exercises one unit of authority, so the
	// operation binding is sealed.
	FormOperation Form = "operation"
	// FormDelegated is one delegation hop: an actor acting on the owner's
	// authority, narrowed.
	FormDelegated Form = "delegated"
	// FormDelegatedOperation is a delegation hop that exercises one unit of
	// authority.
	FormDelegatedOperation Form = "delegated-operation"
	// FormGrant is the single-use capability an approval justifies.
	FormGrant Form = "grant"
	// FormForeign is not a capability this package mints at all: a token in
	// another encoding, which a conforming verifier refuses as such.
	FormForeign Form = "foreign"
)

// Outcome is what a conforming verifier must reach for a fixture.
type Outcome string

const (
	// OutcomeAccepted means verification returns no error.
	OutcomeAccepted Outcome = "accepted"
	// OutcomeRejected means verification returns an error matching Fixture.Err.
	OutcomeRejected Outcome = "rejected"
)

// Fixture is one conformance token: the bytes as they would be presented, the
// outcome a conforming verifier must reach, and the sentinel a rejection must
// match.
type Fixture struct {
	// Name identifies the fixture in a failure message.
	Name string

	// Form is which shape of capability it is.
	Form Form

	// Token is the signed capability, exactly as a caller would present it.
	Token string

	// Outcome is the required result.
	Outcome Outcome

	// Err is the sentinel a rejection must match with errors.Is, and is nil
	// for an accepted fixture. It is part of the contract rather than
	// decoration: "refused" and "refused for the stated reason" are different
	// guarantees, and the look-alike fixture exists precisely because one
	// implementation refused the right token for the wrong reason.
	Err error

	// Reason says which rule decides the outcome.
	Reason string

	// Message is a substring the refusal's message must contain, for the
	// fixtures whose sentinel is shared with others. ErrInvalid is the
	// umbrella for several unrelated refusals, so a fixture that exists to be
	// one of them — the tampered payload, which must be a SIGNATURE failure —
	// would otherwise pass for any of the rest. Empty means the sentinel is
	// the whole contract.
	Message string

	// SingleUse marks a capability a conforming verifier must consume: a
	// second presentation is refused with ErrReplayed.
	SingleUse bool

	// NeedsIssuer marks a fixture whose stated Outcome is reachable ONLY by a
	// verifier holding the issuer's own records — today, the approvals
	// engine's record of a grant.
	//
	// It exists so the kit can certify the verify-only entrypoint without
	// certifying a downgrade. A full verifier must reach Outcome for every
	// fixture. An Authenticator holds no grant records, so for a fixture
	// marked here it must refuse with ErrNeedsIssuer — and a weaker
	// authenticator, one that let an unchecked approval through, ACCEPTS this
	// fixture and fails. Without the mark, a kit run against an Authenticator
	// would have had to be lenient about exactly the case that matters.
	NeedsIssuer bool

	// NeedsLiveState marks a fixture whose stated Outcome is reachable ONLY
	// against the issuer's live state — the authorization revision, the
	// sealed installation's revision, a principal's epoch, the approved build,
	// an operation binding's revision, incarnation, withdrawal or grant
	// association. Every such refusal is ErrRevoked, and every ErrRevoked
	// refusal is one of these; a test holds the two sets equal.
	//
	// It exists so the kit can certify the forwarding hop's entrypoint
	// without certifying a pretence. A hop holds no live state, so for a
	// fixture marked here Inspect must ACCEPT — forward it, and leave the
	// refusal to the callee that holds the state. An inspector that refused
	// one would be claiming a check it cannot make, which is the mirror image
	// of the authenticator's downgrade: not accepting what it never checked,
	// but refusing on what it never read. The kit catches both directions.
	NeedsLiveState bool
}

// FixtureKeyPair returns the fixture signing key. THE PRIVATE KEY IS PUBLIC —
// it is derived from a seed written in this file, so anyone can produce it. It
// is exported because minting a fixture needs it and because a consumer
// building a negative case of its own should use the same key rather than
// inventing a second one. Nothing outside this kit may trust it.
func FixtureKeyPair() (ed25519.PublicKey, ed25519.PrivateKey) {
	material := sha256.Sum256([]byte(fixtureSeed))
	private := ed25519.NewKeyFromSeed(material[:])
	public, ok := private.Public().(ed25519.PublicKey)
	if !ok {
		panic("work context fixtures: ed25519 private key did not yield an ed25519 public key")
	}
	return public, private
}

// FixtureKeys is the key map a conforming verifier is configured with.
func FixtureKeys() map[string]ed25519.PublicKey {
	public, _ := FixtureKeyPair()
	return map[string]ed25519.PublicKey{FixtureKeyID: public}
}

// FixtureRevisions is the authorization revision source a conforming verifier
// is configured with.
func FixtureRevisions() RevisionSource { return FixedRevision(FixtureAuthorizationRevision) }

// FixtureSeals is the seal source a conforming verifier is configured with,
// preloaded with the live values every accepted fixture is sealed to.
func FixtureSeals() *MemorySealSource {
	source := NewMemorySealSource()
	if err := source.Put(FixturePrincipal, Seal{
		InstallationID:       FixtureInstallation,
		InstallationRevision: FixtureInstallationRevision,
	}); err != nil {
		panic(err)
	}
	// The approved execution is held per PRINCIPAL now, not on the
	// installation seal, which is what lets a delegated hop attest its own.
	if err := source.PutApprovedBuild(FixturePrincipal, FixtureImageDigest, FixtureBuildIncarnation); err != nil {
		panic(err)
	}
	// The owner's epoch is recorded like every other principal's, through the
	// one writer. Put no longer does it as a side effect: that side effect was
	// a second source of truth for the epoch, and recording an unrelated
	// installation could lower it.
	if err := source.PutEpoch(FixturePrincipal, FixturePrincipalEpoch); err != nil {
		panic(err)
	}
	if err := source.PutEpoch(FixtureActor, FixtureActorEpoch); err != nil {
		panic(err)
	}
	if err := source.PutEpoch(FixtureApprover, 1); err != nil {
		panic(err)
	}
	// Every delegated principal in the kit is an AGENT, and an agent runs an
	// approved build. They carried no execution record at all, which was
	// readable as "bears no execution" only because an unknown principal used
	// to answer that — so the kit's delegated fixtures demonstrated a hop with
	// an empty execution and nothing objected.
	for _, principal := range []string{FixtureActor, FixtureApprover} {
		if err := source.PutApprovedBuild(principal, FixtureImageDigest, FixtureBuildIncarnation); err != nil {
			panic(err)
		}
	}
	for _, binding := range []OperationBinding{
		// Granted to the OWNER, for the session fixtures.
		{
			ID: FixtureBindingID, PrincipalID: FixturePrincipal, InstallationID: FixtureInstallation,
			Revision: FixtureBindingRevision, Incarnation: FixtureBindingIncarnation,
		},
		// Granted to the ACTOR, for the delegated-operation fixture: a
		// delegated hop exercises a binding granted to the hop's principal,
		// not to the owner.
		{
			ID: FixtureActorBindingID, PrincipalID: FixtureActor, InstallationID: FixtureInstallation,
			Revision: FixtureBindingRevision, Incarnation: FixtureBindingIncarnation,
		},
		// A binding that EXISTS and is granted to someone else, so "sealed to
		// a binding the caller does not hold" is distinguishable from "sealed
		// to a binding that does not exist".
		{
			ID: FixtureOtherBindingID, PrincipalID: "principal-stranger", InstallationID: FixtureInstallation,
			Revision: 1, Incarnation: 1,
		},
		// Granted to the OWNER, at a revision the wrong-revision fixture is
		// minted behind — so that fixture varies the revision and nothing
		// else.
		{
			ID: FixtureMovedRevisionBindingID, PrincipalID: FixturePrincipal, InstallationID: FixtureInstallation,
			Revision: FixtureBindingRevision + 1, Incarnation: FixtureBindingIncarnation,
		},
		// And one in another installation, for the same reason one axis over.
		{
			ID: FixtureForeignInstallationBindingID, PrincipalID: FixturePrincipal, InstallationID: "installation-conformance-other",
			Revision: 1, Incarnation: 1,
		},
		// Withdrawn. Its counters MATCH what a capability sealed to it
		// carries, so only the withdrawal refuses it.
		{
			ID: FixtureRevokedBindingID, PrincipalID: FixturePrincipal, InstallationID: FixtureInstallation,
			Revision: FixtureBindingRevision, Incarnation: FixtureBindingIncarnation, Revoked: true,
		},
		// Withdrawn and re-created: SAME revision, higher incarnation. The
		// incarnation is what separates the new binding from the old one, and
		// a verifier comparing only the revision accepts the stale capability.
		{
			ID: FixtureReincarnatedBindingID, PrincipalID: FixturePrincipal, InstallationID: FixtureInstallation,
			Revision: FixtureBindingRevision, Incarnation: FixtureBindingIncarnation + 1,
		},
	} {
		if err := source.PutBinding(binding); err != nil {
			panic(err)
		}
	}
	return source
}

// FixtureGrant is the issuer's record of the approval the grant fixture
// carries. Its window opens at the given instant so a verifier checking it
// against its own clock finds it open.
func FixtureGrant(now time.Time) *Grant {
	return &Grant{
		ID:                    FixtureGrantID,
		Approvers:             []Approver{{PrincipalID: FixtureApprover, Kind: "human"}},
		Scope:                 fixtureScope("record", "approve", "record-conformance"),
		Subject:               FixtureGrantSubject,
		RequestDigest:         FixtureGrantRequestDigest,
		Audience:              FixtureAudience,
		NotAfter:              now.Add(time.Hour),
		AuthorizationRevision: FixtureAuthorizationRevision,
	}
}

// FixtureGrants is the grant source a conforming verifier is configured with.
func FixtureGrants(now time.Time) GrantSource { return fixtureGrantSource{now: now} }

type fixtureGrantSource struct{ now time.Time }

func (s fixtureGrantSource) Grant(_ context.Context, id string) (*Grant, error) {
	if id != FixtureGrantID {
		return nil, fmt.Errorf("work context: no conformance grant %q", id)
	}
	return FixtureGrant(s.now), nil
}

// fixtureScope builds one scope. An empty id means EVERY resource of the kind,
// which is an absent ResourceIds and not a list holding an empty string: the
// latter is an explicit id that happens to be empty, which a child narrowing
// the wildcard would be widening instead.
func fixtureScope(kind, action, id string) *basev0.WorkScopeV1 {
	scope := &basev0.WorkScopeV1{ResourceKind: kind, Actions: []string{action}}
	if id != "" {
		scope.ResourceIds = []string{id}
	}
	return scope
}

// FixtureForeignToken is a token in the encoding a second implementation once
// used: "<base64url payload>.<base64url signature>" with a hand-written JSON
// payload, Ed25519-signed with the fixture key so the signature is genuine.
//
// It is the most important fixture in the kit. A conforming verifier refuses it
// with ErrNotACoreToken BEFORE checking the signature — and the signature is
// real precisely so that a verifier which checked the signature first would
// still have to refuse it, and would refuse it with the WRONG error. That wrong
// error, "signature does not verify under key X", is what sent people looking
// at key rotation while the actual problem was two encodings. A consumer that
// cannot produce ErrNotACoreToken here is not using this package's verifier.
func FixtureForeignToken() string {
	_, private := FixtureKeyPair()
	// Hand-written, in the shape the other implementation used: snake_case
	// JSON naming the same claims.
	payload := []byte(`{"typ":"codefly.work-context/v1","algorithm":"Ed25519","key_id":"` + FixtureKeyID +
		`","issuer":"` + FixtureIssuer + `","audience":"` + FixtureAudience +
		`","tenant_id":"` + FixtureTenant + `","owner_principal_id":"` + FixturePrincipal +
		`","task_id":"` + FixtureTask + `","session_id":"session-conformance"}`)
	signature := ed25519.Sign(private, payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + base64.RawURLEncoding.EncodeToString(signature)
}

// fixtureAuthority is the minter the kit uses: the fixture key, the fixture
// issuer, and a clock the caller pins so every window is open at verify time.
func fixtureAuthority(now time.Time, seals SealSource) *Authority {
	_, private := FixtureKeyPair()
	return &Authority{
		Issuer:    FixtureIssuer,
		KeyID:     FixtureKeyID,
		Key:       private,
		Revisions: FixtureRevisions(),
		Seals:     seals,
		Now:       func() time.Time { return now },
	}
}

// Fixtures mints the whole kit against the clock the caller passes, which must
// be the one its verifier will check the windows against — in practice
// time.Now(). Every token is freshly minted, so nonces differ between calls and
// two consumers running the kit cannot consume each other's single-use
// capability.
func Fixtures(now time.Time) ([]Fixture, error) {
	live := FixtureSeals()
	authority := fixtureAuthority(now, live)
	ctx := context.Background()

	session, verifiedSession, err := fixtureSession(ctx, authority, "")
	if err != nil {
		return nil, err
	}
	operation, _, err := fixtureSession(ctx, authority, FixtureBindingID)
	if err != nil {
		return nil, err
	}
	delegated, verifiedDelegated, err := fixtureDelegated(ctx, authority, verifiedSession, "")
	if err != nil {
		return nil, err
	}
	delegatedOperation, _, err := fixtureDelegated(ctx, authority, verifiedSession, FixtureActorBindingID)
	if err != nil {
		return nil, err
	}
	// The grant's subject attests its own execution, on the same terms as a
	// delegation hop: a grant hop is the one hop that may hold authority the
	// previous hop did not, which makes attesting it more important, not less.
	grant, _, err := authority.Grant(ctx, verifiedDelegated, GrantInput{
		Execution: fixtureExecutionFor(ctx, authority, FixtureActor),
		Grant:     FixtureGrant(now), TTL: time.Minute,
	})
	if err != nil {
		return nil, fmt.Errorf("work context fixtures: grant: %w", err)
	}

	fixtures := []Fixture{
		{
			Name: "session", Form: FormSession, Token: session, Outcome: OutcomeAccepted,
			Reason: "the first session of a task: the owner acting directly, sealed to the live installation and build",
		},
		{
			Name: "operation", Form: FormOperation, Token: operation, Outcome: OutcomeAccepted,
			Reason: "a session exercising one unit of authority, with the binding's id, revision and incarnation sealed",
		},
		{
			Name: "delegated", Form: FormDelegated, Token: delegated, Outcome: OutcomeAccepted,
			Reason: "one delegation hop, narrowed, resealed against the issuer's live state rather than the parent's",
		},
		{
			Name: "delegated-operation", Form: FormDelegatedOperation, Token: delegatedOperation, Outcome: OutcomeAccepted,
			Reason: "a delegation hop exercising one unit of authority",
		},
		{
			Name: "grant", Form: FormGrant, Token: grant, Outcome: OutcomeAccepted, SingleUse: true,
			NeedsIssuer: true,
			Reason: "the capability an approval justifies: single-use, so a second presentation is refused as replayed. " +
				"It is the one fixture whose acceptance needs the issuer's own record of the approval, so a verify-only " +
				"entrypoint must refuse it with ErrNeedsIssuer rather than accept an approval it never checked",
		},
		{
			Name: "foreign-encoding", Form: FormForeign, Token: FixtureForeignToken(),
			Outcome: OutcomeRejected, Err: ErrNotACoreToken,
			Reason: "a genuinely signed token in another encoding; it is refused as a foreign format BEFORE the signature " +
				"is checked, because reporting a signature failure for it is the diagnosis that cost the fleet a day",
		},
	}

	negatives, err := fixtureNegatives(ctx, now, verifiedSession, verifiedDelegated)
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, negatives...)
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].Name < fixtures[j].Name })
	return fixtures, nil
}

func fixtureSession(ctx context.Context, authority *Authority, binding string) (string, *Verified, error) {
	return fixtureSessionOn(ctx, authority, FixtureInstallation, binding)
}

// fixtureSessionOn mints a session sealed to a named installation. The
// installation is a parameter because a negative fixture is minted against a
// source that differs from the live one, and "sealed to an installation the
// principal does not hold" is only producible by sealing to one the live
// source has never heard of — which means minting against a source that has.
func fixtureSessionOn(ctx context.Context, authority *Authority, installation, binding string) (string, *Verified, error) {
	// The attested execution is whatever the source being minted AGAINST
	// approves, not the live constants. A negative fixture mints against a
	// divergent source on purpose — that is how "sealed to a replaced build
	// incarnation" is producible at all — and attesting the live values there
	// would make the mint refuse instead of producing the capability the
	// fixture exists to be.
	// Whatever the source being minted AGAINST approves, and NOTHING when it
	// says the principal bears none — which is how the execution-missing
	// fixture is producible at all. This used to fall back to the live
	// constants on any error, so a bears-none source made the mint refuse
	// instead of producing the capability the fixture exists to present.
	attested := fixtureExecutionFor(ctx, authority, FixturePrincipal)
	token, _, err := authority.Start(ctx, StartInput{
		Execution:          attested,
		TenantID:           FixtureTenant,
		OwnerPrincipalID:   FixturePrincipal,
		OwnerPrincipalKind: "human",
		OrganizationID:     FixtureOrganization,
		TaskID:             FixtureTask,
		Audience:           FixtureAudience,
		AuthorityScopes:    []*basev0.WorkScopeV1{fixtureScope("record", "read", "")},
		InstallationID:     installation,
		OperationBindingID: binding,
		TTL:                time.Hour,
	})
	if err != nil {
		return "", nil, fmt.Errorf("work context fixtures: session: %w", err)
	}
	verified, err := fixtureVerify(ctx, authority, token)
	if err != nil {
		return "", nil, err
	}
	return token, verified, nil
}

func fixtureDelegated(ctx context.Context, authority *Authority, parent *Verified, binding string) (string, *Verified, error) {
	token, _, err := authority.Child(ctx, parent, ChildInput{
		// The hop attests the build it is running, which for an agent fixture
		// is the one the fixture issuer approves for it. Read from the source
		// being minted against, for the reason fixtureSessionOn reads it
		// there: a divergent source is how a negative fixture is producible.
		Execution:          fixtureExecutionFor(ctx, authority, FixtureActor),
		PrincipalID:        FixtureActor,
		PrincipalKind:      "agent",
		AgentID:            FixtureAgentID,
		DelegationID:       "delegation-conformance",
		GrantedScopes:      []*basev0.WorkScopeV1{fixtureScope("record", "read", "record-conformance")},
		Audience:           FixtureAudience,
		OperationBindingID: binding,
		TTL:                time.Hour,
	})
	if err != nil {
		return "", nil, fmt.Errorf("work context fixtures: delegated: %w", err)
	}
	verified, err := fixtureVerify(ctx, authority, token)
	if err != nil {
		return "", nil, err
	}
	return token, verified, nil
}

// fixtureVerify turns a minted token back into the Verified an exchange needs.
// It uses a replay store of its own so that building the kit never consumes a
// nonce the consumer's verifier will be asked about.
func fixtureVerify(ctx context.Context, authority *Authority, token string) (*Verified, error) {
	verifier := &Verifier{
		// Building the kit means verifying tokens signed by the kit's key.
		TrustTheConformanceFixtureKey: true,
		Issuer:                        FixtureIssuer,
		Audience:                      FixtureAudience,
		Keys:                          FixtureKeys(),
		Revisions:                     FixtureRevisions(),
		Replay:                        NewMemoryReplayStore(),
		Grants:                        FixtureGrants(authority.now()),
		Seals:                         authority.Seals,
		Now:                           authority.Now,
	}
	verified, err := verifier.Verify(ctx, token)
	if err != nil {
		return nil, fmt.Errorf("work context fixtures: a freshly minted capability did not verify: %w", err)
	}
	return verified, nil
}

// fixtureNegatives mints the refusals. Each is produced by minting against a
// state that differs from the live one in exactly one way, or by rewriting a
// sound capability and re-signing it with the fixture key — so each isolates
// one rule instead of failing for whichever reason is checked first.
func fixtureNegatives(ctx context.Context, now time.Time, session, delegated *Verified) ([]Fixture, error) {
	var fixtures []Fixture

	// Sealed to an installation revision the issuer has moved past. This is
	// the acceptance case the issue names: sealed to revision 3, verifier
	// holds 4 — produced here the other way round, by minting at a revision
	// the live source does not hold.
	for _, moved := range []struct {
		name        string
		seal        Seal
		epoch       uint64
		digest      string
		incarnation uint64
		rule        string
		field       string
	}{
		{
			name: "stale-installation-revision",
			seal: Seal{InstallationID: FixtureInstallation,
				InstallationRevision: FixtureInstallationRevision - 1},
			epoch: FixturePrincipalEpoch, digest: FixtureImageDigest, incarnation: FixtureBuildIncarnation,
			rule: "sealed to an installation revision the issuer has moved past", field: "installation revision",
		},
		{
			name: "future-installation-revision",
			seal: Seal{InstallationID: FixtureInstallation,
				InstallationRevision: FixtureInstallationRevision + 1},
			epoch: FixturePrincipalEpoch, digest: FixtureImageDigest, incarnation: FixtureBuildIncarnation,
			rule:  "sealed to an installation revision ahead of the issuer's; comparison is exact equality, not \"at least\"",
			field: "installation revision",
		},
		{
			name: "stale-principal-epoch",
			seal: Seal{InstallationID: FixtureInstallation,
				InstallationRevision: FixtureInstallationRevision},
			epoch: FixturePrincipalEpoch - 1, digest: FixtureImageDigest, incarnation: FixtureBuildIncarnation,
			rule: "sealed to a superseded principal epoch", field: "principal epoch",
		},
		{
			name: "stale-build-incarnation",
			seal: Seal{InstallationID: FixtureInstallation,
				InstallationRevision: FixtureInstallationRevision},
			epoch: FixturePrincipalEpoch, digest: FixtureImageDigest, incarnation: FixtureBuildIncarnation - 1,
			rule: "sealed to a replaced build incarnation", field: "build incarnation",
		},
		{
			// THE APPROVED BUILD ITSELF, which the kit did not cover: a review
			// deleted the verifier's digest comparison and the kit stayed green,
			// because every fixture carried the approved digest and so none could
			// tell the check from its absence. The incarnation MATCHES here on
			// purpose, so a verifier comparing only counters accepts it.
			name: "unapproved-build",
			seal: Seal{InstallationID: FixtureInstallation,
				InstallationRevision: FixtureInstallationRevision},
			epoch: FixturePrincipalEpoch, digest: FixtureOtherImageDigest, incarnation: FixtureBuildIncarnation,
			rule: "sealed to a build the issuer does not approve, at the live incarnation", field: "image digest",
		},
		{
			name: "unknown-installation",
			seal: Seal{InstallationID: "installation-conformance-other",
				InstallationRevision: FixtureInstallationRevision},
			epoch: FixturePrincipalEpoch, digest: FixtureImageDigest, incarnation: FixtureBuildIncarnation,
			rule: "sealed to an installation the principal does not hold", field: "installation",
		},
	} {
		divergent := NewMemorySealSource()
		if err := divergent.Put(FixturePrincipal, moved.seal); err != nil {
			return nil, err
		}
		if err := divergent.PutEpoch(FixturePrincipal, moved.epoch); err != nil {
			return nil, err
		}
		if err := divergent.PutApprovedBuild(FixturePrincipal, moved.digest, moved.incarnation); err != nil {
			return nil, err
		}
		for _, binding := range []OperationBinding{
			{
				ID: FixtureBindingID, PrincipalID: FixturePrincipal, InstallationID: moved.seal.InstallationID,
				Revision: FixtureBindingRevision, Incarnation: FixtureBindingIncarnation,
			},
		} {
			if err := divergent.PutBinding(binding); err != nil {
				return nil, err
			}
		}
		token, _, err := fixtureSessionOn(ctx, fixtureAuthority(now, divergent), moved.seal.InstallationID, "")
		if err != nil {
			return nil, err
		}
		fixtures = append(fixtures, Fixture{
			Name: moved.name, Form: FormSession, Token: token,
			Outcome: OutcomeRejected, Err: ErrRevoked, NeedsLiveState: true, Reason: moved.rule,
		})
	}

	// THE CORRESPONDENCE FROM THE OTHER SIDE, which no fixture covered: a
	// capability carrying NO execution whose exercising principal the live
	// issuer approves a build for. That is the shape a minter which forgot to
	// stamp the execution produces, and only the "carries none" branch
	// refuses it. Minted against a source where the owner bears no execution.
	bearsNone := NewMemorySealSource()
	if err := bearsNone.Put(FixturePrincipal, Seal{
		InstallationID: FixtureInstallation, InstallationRevision: FixtureInstallationRevision,
	}); err != nil {
		return nil, err
	}
	if err := bearsNone.PutEpoch(FixturePrincipal, FixturePrincipalEpoch); err != nil {
		return nil, err
	}
	if err := bearsNone.PutBearsNoExecution(FixturePrincipal); err != nil {
		return nil, err
	}
	if err := bearsNone.PutBinding(OperationBinding{
		ID: FixtureBindingID, PrincipalID: FixturePrincipal, InstallationID: FixtureInstallation,
		Revision: FixtureBindingRevision, Incarnation: FixtureBindingIncarnation,
	}); err != nil {
		return nil, err
	}
	noExecution, _, err := fixtureSessionOn(ctx, fixtureAuthority(now, bearsNone), FixtureInstallation, "")
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "execution-missing", Form: FormSession, Token: noExecution,
		Outcome: OutcomeRejected, Err: ErrRevoked, NeedsLiveState: true,
		Reason: "carries no execution, and the issuer approves a build for the principal exercising it",
	})

	// Sealed to the wrong binding: one the issuer DOES hold, so the refusal is
	// "this capability is sealed to a binding whose revision does not match"
	// and not "no such binding". A verifier that searched the bindings for one
	// fitting the capability's scopes would accept this.
	divergent := NewMemorySealSource()
	if err := divergent.Put(FixturePrincipal, Seal{
		InstallationID: FixtureInstallation, InstallationRevision: FixtureInstallationRevision,
	}); err != nil {
		return nil, err
	}
	if err := divergent.PutEpoch(FixturePrincipal, FixturePrincipalEpoch); err != nil {
		return nil, err
	}
	if err := divergent.PutApprovedBuild(FixturePrincipal, FixtureImageDigest, FixtureBuildIncarnation); err != nil {
		return nil, err
	}
	// Granted to the OWNER at the SAME installation, differing from the live
	// source in the REVISION ALONE — which is what makes this fixture about
	// the revision.
	//
	// It used FixtureOtherBindingID, which the live source grants to another
	// principal: so entitlement AND revision both differed, and entitlement is
	// checked first, so the refusal was the entitlement while this comment
	// claimed the revision. One fixture, one variable.
	if err := divergent.PutBinding(OperationBinding{
		ID: FixtureMovedRevisionBindingID, PrincipalID: FixturePrincipal, InstallationID: FixtureInstallation,
		Revision: FixtureBindingRevision, Incarnation: FixtureBindingIncarnation,
	}); err != nil {
		return nil, err
	}
	wrongBinding, _, err := fixtureSessionOn(ctx, fixtureAuthority(now, divergent), FixtureInstallation, FixtureMovedRevisionBindingID)
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "wrong-binding-revision", Form: FormOperation, Token: wrongBinding,
		Outcome: OutcomeRejected, Err: ErrRevoked, NeedsLiveState: true,
		Reason: "sealed to a binding at a revision the issuer does not hold; the binding exists, so a verifier " +
			"that searched for a binding fitting the capability's scopes would accept it",
	})

	// Rewritten capabilities, re-signed with the fixture key so the signature
	// is genuine and the rule being tested is the one that refuses them.
	_, private := FixtureKeyPair()
	resign := func(wc *basev0.WorkContextV1) (string, error) {
		payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(wc)
		if err != nil {
			return "", err
		}
		return base64.RawURLEncoding.EncodeToString(payload) + "." +
			base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, payload)), nil
	}

	unsealed := cloneClaims(session.Context())
	unsealed.Seal = nil
	unsealedToken, err := resign(unsealed)
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "missing-seal", Form: FormSession, Token: unsealedToken,
		Outcome: OutcomeRejected, Err: ErrInvalid,
		Reason: "a genuinely signed capability carrying no seal. The SCHEMA refuses it: the seal was optional once, " +
			"with archived receipts as the reason, and two reviews named that a compatibility hedge the rules forbid",
	})

	noInstallation := cloneClaims(session.Context())
	noInstallation.Seal = cloneSeal(session.Context().GetSeal())
	noInstallation.Seal.InstallationId = ""
	noInstallationToken, err := resign(noInstallation)
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "seal-without-installation", Form: FormSession, Token: noInstallationToken,
		Outcome: OutcomeRejected, Err: ErrInvalid,
		Reason: "a seal naming no installation is bound to nothing. The schema refuses it, which it may do without " +
			"invalidating anything archived: the seal itself is optional, so a capability from before it existed " +
			"carries none and is unaffected — only a capability that carries one must carry a whole one",
	})

	// A tampered payload: one byte of the sealed installation revision changed,
	// and NOT re-signed. This is the fixture whose refusal must be a signature
	// failure — which is what makes the foreign-encoding fixture's different
	// error meaningful rather than cosmetic.
	_, sessionSignature, _ := strings.Cut(session.Encoded(), ".")
	tampered, err := fixtureTamper(session.Context(), sessionSignature)
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "tampered-payload", Form: FormSession, Token: tampered,
		Outcome: OutcomeRejected, Err: ErrInvalid, Message: "signature does not verify",
		Reason: "a sound capability with one byte of its payload changed and the signature left alone; this is what a " +
			"signature failure is FOR, and the one fixture that must report one",
	})

	// Minted for another audience. A capability is not a credential anywhere
	// but the audience it names.
	otherAudience := cloneClaims(delegated.Context())
	otherAudience.Audience = "codefly.test/elsewhere"
	otherAudienceToken, err := resign(otherAudience)
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "another-audience", Form: FormDelegated, Token: otherAudienceToken,
		Outcome: OutcomeRejected, Err: ErrInvalid, Message: "presented to",
		Reason: "minted for another audience; a capability is not a credential outside the one it names",
	})

	// Addressed to NO audience. The schema refuses it (min_len 1), so every
	// entrypoint refuses it in the decode path, before any comparison — which
	// is the property that matters at a forwarding hop: a host's gateway once
	// held an empty-string sentinel meaning "do not check the audience", and a
	// capability minted with an empty audience would have matched it. There is
	// no such capability, and this fixture is what holds that.
	noAudience := cloneClaims(session.Context())
	noAudience.Audience = ""
	noAudienceToken, err := resign(noAudience)
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "empty-audience", Form: FormSession, Token: noAudienceToken,
		Outcome: OutcomeRejected, Err: ErrInvalid, Message: "audience",
		Reason: "addressed to no audience at all; the schema refuses it before any entrypoint compares it to anything, " +
			"so an empty expectation at a hop can never be matched by a capability that names nothing",
	})

	// Signed by a key the verifier does not hold.
	foreignKeyToken, err := fixtureForeignKey(session.Context())
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "unknown-key", Form: FormSession, Token: foreignKeyToken,
		Outcome: OutcomeRejected, Err: ErrInvalid, Message: "no verification key",
		Reason: "a capability naming a key id the verifier does not hold, signed by that key; the refusal is the " +
			"key LOOKUP and not a signature mismatch, which tampered-payload already covers",
	})

	// A delegated capability whose ACTOR's epoch has moved. The owner's seal
	// is current, so this is refused only because the actor is checked
	// separately — which is the whole point of the actor epoch: revoking a
	// delegated principal must reach the capabilities it acts in without
	// cutting off the owner or the tenant.
	staleActor := NewMemorySealSource()
	if err := staleActor.Put(FixturePrincipal, Seal{
		InstallationID: FixtureInstallation, InstallationRevision: FixtureInstallationRevision,
	}); err != nil {
		return nil, err
	}
	if err := staleActor.PutEpoch(FixturePrincipal, FixturePrincipalEpoch); err != nil {
		return nil, err
	}
	if err := staleActor.PutApprovedBuild(FixturePrincipal, FixtureImageDigest, FixtureBuildIncarnation); err != nil {
		return nil, err
	}
	if err := staleActor.PutEpoch(FixtureActor, FixtureActorEpoch-1); err != nil {
		return nil, err
	}
	if err := staleActor.PutApprovedBuild(FixtureActor, FixtureImageDigest, FixtureBuildIncarnation); err != nil {
		return nil, err
	}
	staleActorAuthority := fixtureAuthority(now, staleActor)
	_, staleParent, err := fixtureSession(ctx, staleActorAuthority, "")
	if err != nil {
		return nil, err
	}
	staleActorToken, _, err := fixtureDelegated(ctx, staleActorAuthority, staleParent, "")
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "stale-actor-epoch", Form: FormDelegated, Token: staleActorToken,
		Outcome: OutcomeRejected, Err: ErrRevoked, NeedsLiveState: true,
		Reason: "the delegated actor's epoch has moved while the owner's seal is current; without a per-actor epoch " +
			"the only lever for a compromised actor is the tenant's authorization revision, which cuts off every " +
			"capability of the tenant",
	})

	// A delegated capability whose hop carries NO epoch. The schema cannot
	// require it without invalidating every archived capability, so the
	// verifier is what makes it required — a hop with no epoch is a principal
	// that cannot be revoked.
	noEpoch := cloneClaims(delegated.Context())
	noEpoch.ActorChain[len(noEpoch.ActorChain)-1].PrincipalEpoch = 0
	noEpochToken, err := resign(noEpoch)
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "actor-without-epoch", Form: FormDelegated, Token: noEpochToken,
		Outcome: OutcomeRejected, Err: ErrInvalid,
		Reason: "a genuinely signed delegated capability whose actor hop carries no epoch, so that principal could " +
			"never be revoked. The schema refuses it now, with required plus gte=1",
	})

	// Sealed to a binding that EXISTS, at the right revision, in the right
	// installation — and is granted to someone else. An opaque ID is not
	// authorization, and a verifier that resolved the ID and compared only its
	// counters would accept this.
	for _, foreign := range []struct {
		name    string
		binding string
		rule    string
	}{
		{
			name: "binding-of-another-principal", binding: FixtureOtherBindingID,
			rule: "sealed to a binding granted to a different principal; it exists and its counters match, so only the " +
				"grant association refuses it",
		},
		{
			name: "binding-in-another-installation", binding: FixtureForeignInstallationBindingID,
			rule: "sealed to a binding granted within a different installation; the installation half of the " +
				"association is what refuses it",
		},
	} {
		// Minted against a source that grants it here, so the mint succeeds
		// and the LIVE source is what refuses it.
		permissive := NewMemorySealSource()
		if err := permissive.Put(FixturePrincipal, Seal{
			InstallationID: FixtureInstallation, InstallationRevision: FixtureInstallationRevision,
		}); err != nil {
			return nil, err
		}
		if err := permissive.PutApprovedBuild(FixturePrincipal, FixtureImageDigest, FixtureBuildIncarnation); err != nil {
			return nil, err
		}
		if err := permissive.PutEpoch(FixturePrincipal, FixturePrincipalEpoch); err != nil {
			return nil, err
		}
		live, err := FixtureSeals().OperationBinding(ctx, foreign.binding)
		if err != nil {
			return nil, err
		}
		if err := permissive.PutBinding(OperationBinding{
			ID: foreign.binding, PrincipalID: FixturePrincipal, InstallationID: FixtureInstallation,
			Revision: live.Revision, Incarnation: live.Incarnation,
		}); err != nil {
			return nil, err
		}
		token, _, err := fixtureSessionOn(ctx, fixtureAuthority(now, permissive), FixtureInstallation, foreign.binding)
		if err != nil {
			return nil, err
		}
		fixtures = append(fixtures, Fixture{
			Name: foreign.name, Form: FormOperation, Token: token,
			Outcome: OutcomeRejected, Err: ErrRevoked, NeedsLiveState: true, Reason: foreign.rule,
		})
	}

	// Zeroes and a half-filled binding. A consumer was constructing these
	// itself by re-signing a minted capability with the fixture key — which
	// means every consumer that wants them writes an ed25519.Sign of its own,
	// and a kit that leaves a refusal out invites exactly that. They are
	// ErrInvalid because the SCHEMA refuses them: zero is not an epoch, a
	// revision or an incarnation, and a binding id with no revision is half a
	// binding.
	for _, malformed := range []struct {
		name  string
		rule  string
		apply func(*basev0.WorkContextV1)
	}{
		{
			name:  "zero-principal-epoch",
			rule:  "a seal carrying epoch 0, which names no epoch and would compare equal to a source holding nothing",
			apply: func(wc *basev0.WorkContextV1) { wc.Seal.PrincipalEpoch = 0 },
		},
		{
			name:  "zero-installation-revision",
			rule:  "a seal carrying installation revision 0",
			apply: func(wc *basev0.WorkContextV1) { wc.Seal.InstallationRevision = 0 },
		},
		{
			// THE PAIRING RULE, which had no fixture: one execution field
			// without the other names a run nothing can check, and the schema
			// refuses it before any state is consulted.
			name: "seal-half-execution",
			rule: "a seal carrying an image digest with no incarnation",
			apply: func(wc *basev0.WorkContextV1) {
				digest := FixtureImageDigest
				wc.Seal.ImageDigest = &digest
				wc.Seal.BuildIncarnation = nil
			},
		},
		{
			name: "zero-build-incarnation",
			rule: "a seal carrying build incarnation 0",
			apply: func(wc *basev0.WorkContextV1) {
				zero := uint64(0)
				wc.Seal.BuildIncarnation = &zero
			},
		},
		{
			name: "partial-operation-binding",
			rule: "an operation binding naming an id with no revision and no incarnation; half a binding is not a binding",
			apply: func(wc *basev0.WorkContextV1) {
				wc.OperationBinding = &basev0.WorkOperationBindingV1{BindingId: FixtureBindingID}
			},
		},
	} {
		altered := cloneClaims(session.Context())
		altered.Seal = cloneSeal(session.Context().GetSeal())
		malformed.apply(altered)
		token, err := resign(altered)
		if err != nil {
			return nil, err
		}
		fixtures = append(fixtures, Fixture{
			Name: malformed.name, Form: FormSession, Token: token,
			Outcome: OutcomeRejected, Err: ErrInvalid, Reason: malformed.rule,
		})
	}

	// The window, the issuer and the revision. A verifier that skipped expiry
	// entirely passed this kit, which made its claim to cover "every way one
	// is refused" false.
	expired := cloneClaims(session.Context())
	expired.NotBeforeUnix = now.Add(-2 * time.Hour).Unix()
	expired.ExpiresAtUnix = now.Add(-time.Hour).Unix()
	expiredToken, err := resign(expired)
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "expired", Form: FormSession, Token: expiredToken,
		Outcome: OutcomeRejected, Err: ErrInvalid, Message: "expired at",
		Reason: "a sound capability whose window has closed; a verifier that never checks expiry passes every other fixture",
	})

	notYet := cloneClaims(session.Context())
	notYet.NotBeforeUnix = now.Add(time.Hour).Unix()
	notYet.ExpiresAtUnix = now.Add(2 * time.Hour).Unix()
	notYetToken, err := resign(notYet)
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "not-yet-valid", Form: FormSession, Token: notYetToken,
		Outcome: OutcomeRejected, Err: ErrInvalid, Message: "not valid before",
		Reason: "a capability whose window has not opened; not_before is a bound, not decoration",
	})

	otherIssuer := cloneClaims(session.Context())
	otherIssuer.Issuer = "https://authority.elsewhere.test"
	otherIssuerToken, err := resign(otherIssuer)
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "another-issuer", Form: FormSession, Token: otherIssuerToken,
		Outcome: OutcomeRejected, Err: ErrInvalid, Message: "issued by",
		Reason: "signed by a key the verifier holds and naming another issuer; the key is not the trust decision",
	})

	superseded := cloneClaims(session.Context())
	superseded.AuthorizationRevision = FixtureAuthorizationRevision - 1
	supersededToken, err := resign(superseded)
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "superseded-authorization-revision", Form: FormSession, Token: supersededToken,
		Outcome: OutcomeRejected, Err: ErrRevoked, NeedsLiveState: true,
		Reason: "minted at a revision the issuer has moved past; the coarse revocation lever, which nothing else in the kit exercised",
	})

	// A revoked binding and a re-created one. Each is minted while the binding
	// was still live — against a source that grants it soundly — so the LIVE
	// source is what refuses it. Minting against the live state instead would
	// fail at the mint and isolate nothing.
	for _, moved := range []struct {
		name string
		id   string
		rule string
	}{
		{
			name: "revoked-operation-binding", id: FixtureRevokedBindingID,
			rule: "sealed to a binding the issuer has withdrawn; its counters still MATCH, so only the withdrawal refuses it",
		},
		{
			name: "wrong-binding-incarnation", id: FixtureReincarnatedBindingID,
			rule: "sealed to a binding that was withdrawn and re-created at the same revision; the incarnation is what " +
				"separates the new binding from the old one, and a verifier comparing only the revision accepts this",
		},
	} {
		permissive := NewMemorySealSource()
		if err := permissive.Put(FixturePrincipal, Seal{
			InstallationID: FixtureInstallation, InstallationRevision: FixtureInstallationRevision,
		}); err != nil {
			return nil, err
		}
		if err := permissive.PutApprovedBuild(FixturePrincipal, FixtureImageDigest, FixtureBuildIncarnation); err != nil {
			return nil, err
		}
		if err := permissive.PutEpoch(FixturePrincipal, FixturePrincipalEpoch); err != nil {
			return nil, err
		}
		if err := permissive.PutBinding(OperationBinding{
			ID: moved.id, PrincipalID: FixturePrincipal, InstallationID: FixtureInstallation,
			Revision: FixtureBindingRevision, Incarnation: FixtureBindingIncarnation,
		}); err != nil {
			return nil, err
		}
		token, _, err := fixtureSessionOn(ctx, fixtureAuthority(now, permissive), FixtureInstallation, moved.id)
		if err != nil {
			return nil, err
		}
		fixtures = append(fixtures, Fixture{
			Name: moved.name, Form: FormOperation, Token: token,
			Outcome: OutcomeRejected, Err: ErrRevoked, NeedsLiveState: true, Reason: moved.rule,
		})
	}

	// A genuinely signed capability carrying an UNKNOWN FIELD, nested inside
	// the seal rather than at the top level — because a check that looked only
	// at the root would pass it.
	//
	// This is the wire-level half of the one-implementation rule. A second
	// minter using the same field numbers, or adding one of its own, produced
	// a token that verified and passed this whole kit, because the signature
	// covers whatever bytes were presented.
	foreignField := cloneClaims(session.Context())
	foreignField.Seal = cloneSeal(session.Context().GetSeal())
	foreignField.Seal.ProtoReflect().SetUnknown(fixtureUnknownField(4095, "a field this Core does not know"))
	foreignFieldToken, err := resign(foreignField)
	if err != nil {
		return nil, err
	}
	fixtures = append(fixtures, Fixture{
		Name: "unknown-field", Form: FormSession, Token: foreignFieldToken,
		Outcome: OutcomeRejected, Err: ErrInvalid, Message: "unknown field",
		Reason: "a genuinely signed capability carrying a field this Core does not know, nested inside the seal. " +
			"Accepting it is accepting a second implementation at the wire, and it lets data ride inside a signed " +
			"credential that nothing here reads",
	})

	// Not the "<payload>.<signature>" shape at all.
	for name, token := range map[string]string{
		"no-separator":    base64.RawURLEncoding.EncodeToString([]byte("nonsense")),
		"payload-not-b64": "not base64!." + base64.RawURLEncoding.EncodeToString([]byte("sig")),
		"empty-token":     "",
		"separator-only":  ".",
	} {
		fixtures = append(fixtures, Fixture{
			Name: name, Form: FormForeign, Token: token,
			Outcome: OutcomeRejected, Err: ErrInvalid,
			Reason: "not the token shape; refused before anything is trusted",
		})
	}
	return fixtures, nil
}

// fixtureTamper changes one SEALED VALUE and keeps the original signature, so
// the result is schema-valid and the only thing wrong with it is the
// signature.
//
// It used to flip the payload's last byte, on the reasoning that the last
// bytes are inside the sealed values. That broke the moment image_digest was
// added as the seal's last field: the flipped byte landed inside a string with
// a pattern rule, so protovalidate refused the token and the fixture whose
// entire job is to be the one SIGNATURE failure started failing for a schema
// reason instead. A fixture that can stop testing what it is named for when an
// unrelated field is added is the wrong construction.
//
// Changing a field and re-marshalling, while keeping the old signature,
// cannot drift that way: the claims are always valid and the signature is
// always wrong.
func fixtureTamper(wc *basev0.WorkContextV1, signature string) (string, error) {
	altered := cloneClaims(wc)
	// One sealed value, moved to another legitimate one: exactly what a
	// tamperer would reach for, and still schema-valid.
	altered.Seal.InstallationRevision = wc.GetSeal().GetInstallationRevision() + 1
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(altered)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." + signature, nil
}

// fixtureForeignKey signs claims that NAME A KEY ID THE VERIFIER DOES NOT
// HOLD, with the key of that name, so the refusal is the key LOOKUP and not a
// signature mismatch.
//
// The distinction is the fixture's whole value. Keeping the known key id and
// signing with a different private key would exercise signature verification
// — which tampered-payload already covers — and would leave the unknown-key
// path untested.
func fixtureForeignKey(wc *basev0.WorkContextV1) (string, error) {
	material := sha256.Sum256([]byte(fixtureSeed + ": a key no verifier holds"))
	private := ed25519.NewKeyFromSeed(material[:])
	claims := cloneClaims(wc)
	claims.KeyId = "conformance-unheld"
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(claims)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload) + "." +
		base64.RawURLEncoding.EncodeToString(ed25519.Sign(private, payload)), nil
}

// fixtureUnknownField encodes one length-delimited protobuf field the schema
// does not declare, for the fixture that must be refused for carrying one.
func fixtureUnknownField(number int, value string) protoreflect.RawFields {
	var encoded []byte
	if number < 0 {
		panic("work context fixtures: a proto field number is never negative")
	}
	tag := uint64(number)<<3 | 2 // wire type 2: length-delimited
	for tag >= 0x80 {
		encoded = append(encoded, byte(tag)|0x80)
		tag >>= 7
	}
	encoded = append(encoded, byte(tag))
	length := uint64(len(value))
	for length >= 0x80 {
		encoded = append(encoded, byte(length)|0x80)
		length >>= 7
	}
	encoded = append(encoded, byte(length))
	return append(encoded, value...)
}

// isFixtureKey reports whether a verification key is the conformance fixture
// key. A verifier refuses it unless it says otherwise; see
// Verifier.TrustTheConformanceFixtureKey.
//
// It compares in constant time out of habit rather than need — the fixture
// public key is public by construction — because a key comparison that is
// sometimes variable-time is the kind of thing that gets copied.
func isFixtureKey(key ed25519.PublicKey) bool {
	return subtle.ConstantTimeCompare(key, fixturePublicKey[:]) == 1
}

// fixturePublicKey is the fixture key's PUBLIC half, written out rather than
// derived, and that is the whole point of it.
//
// It used to call FixtureKeyPair, which derives the private key from
// fixtureSeed. Verify calls isFixtureKey, so every binary that verifies
// anything linked the seed and the derivation — measured: a binary calling
// Verify contains the seed string and the FixtureKeyPair symbol.
//
// I had reported the opposite, from a control that constructed a Verifier and
// never called Verify, so the linker dropped the whole chain. That control
// does not describe any real consumer: nothing imports this package without
// verifying. The measurement was wrong in the direction of dismissing the
// finding.
//
// Comparing against the public half keeps the refusal — which is the point of
// isFixtureKey — while leaving the seed reachable only from code that actually
// uses the kit. A test asserts these bytes still equal FixtureKeyPair's public
// half, so the constant cannot drift from the key it names.
var fixturePublicKey = [ed25519.PublicKeySize]byte{
	0xcd, 0x49, 0x36, 0x20, 0x71, 0x98, 0xa6, 0x0c, 0xd0, 0x22, 0x0e, 0xb6,
	0x5d, 0x7d, 0xcc, 0xd8, 0x4e, 0x25, 0x8f, 0x09, 0xf7, 0xe2, 0xc8, 0x1d,
	0x1a, 0xb7, 0x86, 0x38, 0xc9, 0x7d, 0x11, 0xfb,
}

// fixtureExecutionFor is the execution a fixture hop attests: whatever the
// source being minted AGAINST approves for that principal, or none when it
// bears none. A negative fixture mints against a divergent source on purpose,
// so attesting the live constants there would make the mint refuse instead of
// producing the capability the fixture exists to present.
func fixtureExecutionFor(ctx context.Context, authority *Authority, principalID string) Execution {
	digest, incarnation, err := authority.Seals.ApprovedBuild(ctx, principalID)
	if err != nil {
		return Execution{}
	}
	return Execution{ImageDigest: digest, BuildIncarnation: incarnation}
}

// cloneClaims is proto.Clone with the assertion checked, because an unchecked
// one is an error the linter is right about even where it cannot fail: a
// fixture that panicked here would say nothing about which fixture.
func cloneClaims(wc *basev0.WorkContextV1) *basev0.WorkContextV1 {
	cloned, ok := proto.Clone(wc).(*basev0.WorkContextV1)
	if !ok {
		panic("work context fixtures: cloning a WorkContextV1 did not yield one")
	}
	return cloned
}

// cloneSeal is cloneClaims for a seal, for the same reason.
func cloneSeal(seal *basev0.WorkSealV1) *basev0.WorkSealV1 {
	cloned, ok := proto.Clone(seal).(*basev0.WorkSealV1)
	if !ok {
		panic("work context fixtures: cloning a WorkSealV1 did not yield one")
	}
	return cloned
}
