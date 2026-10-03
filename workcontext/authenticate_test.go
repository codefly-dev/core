package workcontext_test

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/workcontext"
)

// authenticator is the harness's verify-only entrypoint: the same keys, clock,
// replay store and live sealed state its verifier uses, with the authorization
// revision stated rather than sourced.
func (h *harness) authenticator(aud string) *workcontext.Authenticator {
	return &workcontext.Authenticator{
		Issuer:    issuer,
		Audience:  aud,
		Keys:      map[string]ed25519.PublicKey{keyID: h.public},
		Seals:     h.seals,
		Revisions: h,
		Replay:    h.replay,
		Now:       func() time.Time { return h.clock },
	}
}

// THE property the second entrypoint rests on: for every fixture in the kit
// that does not need the issuer's own records, Authenticate reaches the same
// decision as Verify AND reports it with the same message, byte for byte.
//
// Equal messages is a stronger claim than equal outcomes, and it is the one
// worth asserting: two implementations can agree on accept/refuse for every
// case anyone thought to test and still differ on which rule fired, which is
// precisely the failure ErrNotACoreToken exists because of. Identical text is
// what you get when there is one check path, and it is what breaks the moment
// somebody adds a second.
func TestAuthenticateAndVerifyAgreeOnEveryFixtureIncludingTheMessage(t *testing.T) {
	now := time.Now()
	fixtures, err := workcontext.Fixtures(now)
	require.NoError(t, err)
	require.NotEmpty(t, fixtures)

	clock := func() time.Time { return now }
	seals := workcontext.FixtureSeals()
	keys := workcontext.FixtureKeys()

	// Separate replay stores, because each entrypoint consumes the single-use
	// fixture's nonce and a shared store would make the second one to run see
	// a replay.
	verifierReplay := workcontext.NewMemoryReplayStore()
	verifierReplay.Now = clock
	authenticatorReplay := workcontext.NewMemoryReplayStore()
	authenticatorReplay.Now = clock

	verifier := &workcontext.Verifier{
		TrustTheConformanceFixtureKey: true,
		Issuer:                        workcontext.FixtureIssuer,
		Audience:                      workcontext.FixtureAudience,
		Keys:                          keys,
		Revisions:                     workcontext.FixtureRevisions(),
		Replay:                        verifierReplay,
		Grants:                        workcontext.FixtureGrants(now),
		Seals:                         seals,
		Now:                           clock,
	}
	authenticator := &workcontext.Authenticator{
		TrustTheConformanceFixtureKey: true,
		Issuer:                        workcontext.FixtureIssuer,
		Audience:                      workcontext.FixtureAudience,
		Keys:                          keys,
		Seals:                         seals,
		Revisions:                     workcontext.FixtureRevisions(),
		Replay:                        authenticatorReplay,
		Now:                           clock,
	}

	ctx := context.Background()
	var deferred, agreed int
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			_, verifyErr := verifier.Verify(ctx, fixture.Token)
			_, authErr := authenticator.Authenticate(ctx, fixture.Token)

			if fixture.NeedsIssuer {
				deferred++
				require.NoError(t, verifyErr, "the full verifier holds the issuer's records, so it accepts this")
				require.ErrorIs(t, authErr, workcontext.ErrNeedsIssuer,
					"a verify-only entrypoint must defer this, not accept an approval it never checked")
				return
			}
			agreed++
			if verifyErr == nil {
				require.NoError(t, authErr, "fixture %q verifies; the verify-only entrypoint must accept it too", fixture.Name)
				return
			}
			require.Error(t, authErr)
			require.Equal(t, verifyErr.Error(), authErr.Error(),
				"fixture %q must be refused for the SAME named reason by both entrypoints", fixture.Name)
		})
	}
	require.NotZero(t, deferred, "the kit must mark at least one fixture as needing the issuer's records")
	require.NotZero(t, agreed, "the kit must hold most fixtures to identical outcomes")
}

// A capability carrying an approval is REFUSED, not accepted unchecked. This
// is the one place a verify-only entrypoint could have become a second
// strength, and the direction of the failure is what makes it safe.
func TestAuthenticate_RefusesAGrantCapabilityRatherThanSkippingTheApproval(t *testing.T) {
	h := newHarness(t)
	agent, token, grant := h.elevated(t)

	// The full verifier, holding the grant record, accepts it.
	require.NotNil(t, h.mustVerify(mergeTool, token))

	// Minted fresh, so the refusal below is the missing grant record and not
	// the replay the line above caused: a grant capability is single-use.
	second, _, err := h.authority.Grant(context.Background(), agent, workcontext.GrantInput{Grant: grant, TTL: time.Minute})
	require.NoError(t, err)
	authenticated, err := h.authenticator(mergeTool).Authenticate(context.Background(), second)
	require.Nil(t, authenticated)
	require.ErrorIs(t, err, workcontext.ErrNeedsIssuer)
	require.ErrorContains(t, err, "must be presented to the party that holds those records")

	// And the deferred capability is NOT consumed. This is what makes
	// ErrNeedsIssuer a routing fact rather than a refusal: the caller presents
	// the same capability to the party that holds the grant records, and it
	// must still be spendable there. A grant capability is single-use, so
	// burning its nonce on the way to deferring it would turn "ask someone
	// else" into "this capability is gone" — and the failure would land on the
	// issuer as a replay, pointing at the wrong party entirely.
	require.NotNil(t, h.mustVerify(mergeTool, second),
		"deferring a capability must not spend it; replay consumption happens after every other check for exactly this reason")
}

// An ordinary session authenticates, and the claims come back readable.
func TestAuthenticate_AcceptsASoundSessionAndCarriesItsClaims(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	authenticated, err := h.authenticator(audience).Authenticate(context.Background(), token)
	require.NoError(t, err)
	require.Equal(t, ownerID, authenticated.Context().GetOwnerPrincipalId())
	require.Equal(t, token, authenticated.Encoded())
	require.Equal(t, workcontext.Fingerprint(token), authenticated.SHA256())
	require.Nil(t, authenticated.Actor(), "the owner acts directly")
	require.NotEmpty(t, authenticated.EffectiveScopes())
}

// The seal is compared by the verify-only entrypoint exactly as the full one
// compares it. There is no mode in which it is skipped, so revocation reaches
// a capability presented here too.
func TestAuthenticate_RefusesACapabilitySealedToASupersededInstallationRevision(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)
	authenticator := h.authenticator(audience)

	authenticated, err := authenticator.Authenticate(context.Background(), token)
	require.NoError(t, err)
	require.NotNil(t, authenticated)

	require.NoError(t, h.seals.Put(ownerID, workcontext.Seal{
		ImageDigest: workcontext.FixtureImageDigest, InstallationID: installation,
		InstallationRevision: 4, BuildIncarnation: 11,
	}))
	// The SAME authenticator, reused across the revocation, so one that cached
	// live state after its first success would be caught.
	_, err = authenticator.Authenticate(context.Background(), token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "installation revision")
}

// The delegated actor's epoch is checked here too, per hop.
func TestAuthenticate_RefusesADelegatedCapabilityAfterItsActorIsRevoked(t *testing.T) {
	h := newHarness(t)
	_, owner := h.ownerSession(audience)
	token, _ := h.agentSession(owner, audience)
	authenticator := h.authenticator(audience)

	_, err := authenticator.Authenticate(context.Background(), token)
	require.NoError(t, err)

	require.NoError(t, h.seals.PutEpoch(agentID, 2))
	_, err = authenticator.Authenticate(context.Background(), token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "actor hop 0")
}

// Single-use is still single-use: the replay store is required and consumed.
func TestAuthenticate_ConsumesASingleUseCapability(t *testing.T) {
	h := newHarness(t)
	token, _, err := h.authority.Start(context.Background(), workcontext.StartInput{
		Execution:          workcontext.Execution{ImageDigest: workcontext.FixtureImageDigest, BuildIncarnation: 11},
		InstallationID:     installation,
		TenantID:           tenant,
		OwnerPrincipalID:   ownerID,
		OwnerPrincipalKind: "human",
		OrganizationID:     organization,
		TaskID:             taskID,
		Audience:           audience,
		ReplayPolicy:       workcontext.ReplaySingleUse,
		TTL:                time.Hour,
	})
	require.NoError(t, err)
	authenticator := h.authenticator(audience)

	_, err = authenticator.Authenticate(context.Background(), token)
	require.NoError(t, err)
	_, err = authenticator.Authenticate(context.Background(), token)
	require.ErrorIs(t, err, workcontext.ErrReplayed)
}

// Every input is required, and a missing one is a refusal naming it — never a
// check that quietly stops happening. The seal source and the replay store are
// the two that would be tempting to leave out.
func TestAuthenticate_RefusesWithoutAnyOneOfItsInputs(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	for name, breaks := range map[string]func(*workcontext.Authenticator){
		"no issuer":          func(a *workcontext.Authenticator) { a.Issuer = "" },
		"no audience":        func(a *workcontext.Authenticator) { a.Audience = "" },
		"no keys":            func(a *workcontext.Authenticator) { a.Keys = nil },
		"no seal source":     func(a *workcontext.Authenticator) { a.Seals = nil },
		"no replay store":    func(a *workcontext.Authenticator) { a.Replay = nil },
		"no revision source": func(a *workcontext.Authenticator) { a.Revisions = nil },
	} {
		t.Run(name, func(t *testing.T) {
			authenticator := h.authenticator(audience)
			breaks(authenticator)
			authenticated, err := authenticator.Authenticate(context.Background(), token)
			require.Nil(t, authenticated)
			require.Error(t, err)
		})
	}
}

// The coarse revocation lever reaches this entrypoint, through the same
// per-tenant source the full verifier reads.
func TestAuthenticate_RefusesACapabilityBehindTheIssuersRevision(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)
	authenticator := h.authenticator(audience)

	_, err := authenticator.Authenticate(context.Background(), token)
	require.NoError(t, err)

	h.revision++
	// The SAME authenticator, so one that read the revision once at
	// construction would be caught.
	_, err = authenticator.Authenticate(context.Background(), token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
	require.ErrorContains(t, err, "issuer is at 8")
}

// The revision is per TENANT, by RevisionSource's own signature, and a
// verify-only entrypoint must honour that rather than compare every tenant
// against one number.
//
// This is a host consumer's finding. An earlier draft took a stated
// uint64 here, which against a multi-tenant issuer went on accepting
// capabilities minted at a SUPERSEDED revision for every tenant except the one
// it named — a check that reads as enforced and fires for at most one tenant.
// The test is written with two tenants at different revisions precisely
// because a single-tenant test passes either shape.
func TestAuthenticate_HonoursThePerTenantRevision(t *testing.T) {
	h := newHarness(t)
	token, _ := h.ownerSession(audience)

	byTenant := perTenantRevisions{tenant: 7, "t-other": 99}
	authenticator := h.authenticator(audience)
	authenticator.Revisions = byTenant
	_, err := authenticator.Authenticate(context.Background(), token)
	require.NoError(t, err, "this capability's tenant is at 7 and so is the issuer")

	// Move only the OTHER tenant. A shape that compared every tenant against
	// one number would now refuse this capability.
	byTenant["t-other"] = 100
	_, err = authenticator.Authenticate(context.Background(), token)
	require.NoError(t, err, "another tenant's revision moving must not refuse this tenant's capability")

	// Move this capability's tenant. Now it is superseded.
	byTenant[tenant] = 8
	_, err = authenticator.Authenticate(context.Background(), token)
	require.ErrorIs(t, err, workcontext.ErrRevoked)
}

type perTenantRevisions map[string]uint64

func (r perTenantRevisions) AuthorizationRevision(_ context.Context, tenantID string) (uint64, error) {
	revision, known := r[tenantID]
	if !known {
		return 0, fmt.Errorf("no revision for tenant %q", tenantID)
	}
	return revision, nil
}

// An Authenticated capability cannot become a Verified one, and nothing in the
// package offers a way to make it one.
//
// What this guard is and is not: it guards against REINTRODUCTION. It would
// pass against the commit before this one, where Authenticated did not exist
// at all, so it is not evidence that anything was removed. Its job is to make
// the next addition fail — because that addition is the whole downgrade in one
// function: a caller holding the weaker answer converts it, and every rule
// downstream that requires a *Verified now rests on a question nobody asked.
//
// Checked against the package's exported DECLARATIONS rather than its text, so
// the prose above, which has to name both types to explain the rule, cannot
// fail it.
func TestNoDeclarationTurnsAnAuthenticatedCapabilityIntoAVerifiedOne(t *testing.T) {
	set := token.NewFileSet()
	packages, err := parser.ParseDir(set, ".", func(file os.FileInfo) bool {
		return !strings.HasSuffix(file.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)
	require.Contains(t, packages, "workcontext")

	// names renders a function's receiver and parameters, and its results,
	// as the type names they mention.
	mentions := func(fields *ast.FieldList) []string {
		var found []string
		if fields == nil {
			return found
		}
		for _, field := range fields.List {
			found = append(found, strings.TrimSpace(typeName(field.Type)))
		}
		return found
	}
	for _, file := range packages["workcontext"].Files {
		for _, declaration := range file.Decls {
			function, isFunction := declaration.(*ast.FuncDecl)
			if !isFunction || !function.Name.IsExported() {
				continue
			}
			inputs := append(mentions(function.Recv), mentions(function.Type.Params)...)
			results := mentions(function.Type.Results)
			takesAuthenticated := false
			for _, input := range inputs {
				if input == "*Authenticated" || input == "Authenticated" {
					takesAuthenticated = true
				}
			}
			if !takesAuthenticated {
				continue
			}
			for _, result := range results {
				require.NotContains(t, []string{"*Verified", "Verified"}, result,
					"%s at %s converts an Authenticated capability into a Verified one; see this test's comment",
					function.Name.Name, set.Position(function.Pos()))
			}
		}
	}
}

// typeName renders a type expression as source-like text, enough to recognise
// *Authenticated and *Verified.
func typeName(expression ast.Expr) string {
	switch typed := expression.(type) {
	case *ast.StarExpr:
		return "*" + typeName(typed.X)
	case *ast.Ident:
		return typed.Name
	case *ast.SelectorExpr:
		return typeName(typed.X) + "." + typed.Sel.Name
	case *ast.ArrayType:
		return "[]" + typeName(typed.Elt)
	default:
		return ""
	}
}
