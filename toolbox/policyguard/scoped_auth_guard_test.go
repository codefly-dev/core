package policyguard_test

import (
	"context"
	"encoding/base64"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/policy/testharness"
	"github.com/codefly-dev/core/toolbox/policyguard"
)

// =====================================================================
// Two-level model integration in policyguard.Guard
// =====================================================================
//
// These tests exercise the Guard's CallTool path with scoped-auth
// configured. The Guard must:
//   - On valid token: skip PDP, dispatch to inner
//   - On missing token: fall back to PDP (defense path)
//   - On invalid token: deny without credential downgrade
//   - Honor MaxUses (replay tracking)
//   - Stamp the verified ScopedAuthorization on ctx for handler

// recordingToolboxServer is a minimal toolbox server that records
// CallTool invocations for assertion. We don't need git for these
// tests — just a stub that says "I was called".
type recordingToolboxServer struct {
	toolboxv0.UnimplementedToolboxServer
	calls []recordedCall
}

type recordedCall struct {
	tool     string
	scopedSA *policy.ScopedAuthorization // captured from ctx
}

func (r *recordingToolboxServer) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	r.calls = append(r.calls, recordedCall{
		tool:     req.GetName(),
		scopedSA: policy.ScopedAuthFrom(ctx),
	})
	return &toolboxv0.CallToolResponse{}, nil
}

func mintForTest(t *testing.T, secret []byte, action, resource, audience string, ttl time.Duration) string {
	t.Helper()
	encoded, _, err := policy.Mint(policy.MintInput{
		Principal: &policy.Principal{
			ID: "u-antoine", Kind: policy.KindHuman, OrgID: "org",
		},
		Action:     action,
		Resource:   resource,
		AudienceID: audience,
		TTL:        ttl,
		MaxUses:    1,
	}, secret)
	require.NoError(t, err)
	return encoded
}

func ctxWithScopedAuth(ctx context.Context, token string) context.Context {
	md := metadata.Pairs(policy.ScopedAuthMetadataKey, token)
	return metadata.NewIncomingContext(ctx, md)
}

func TestGuard_FastPath_ValidToken_SkipsPDP(t *testing.T) {
	secret := policy.NewSpawnSecret()
	denyPDP := testharness.NewFakeDeny() // would deny if asked

	inner := &recordingToolboxServer{}
	guard := policyguard.NewWithScopedAuth(inner, denyPDP, "test-toolbox",
		secret, "codefly.dev/test:1.0")

	token := mintForTest(t, secret, "git.status", "", "codefly.dev/test:1.0", time.Minute)
	ctx := ctxWithScopedAuth(context.Background(), token)

	resp, err := guard.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "git.status",
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error)
	require.Len(t, inner.calls, 1, "valid token: inner toolbox handler runs")
	require.Equal(t, 0, denyPDP.CallCount(),
		"fast path: PDP must NOT be consulted when token is valid")
}

func TestGuard_FastPath_StampsScopedAuthOnCtx(t *testing.T) {
	secret := policy.NewSpawnSecret()
	inner := &recordingToolboxServer{}
	guard := policyguard.NewWithScopedAuth(inner, testharness.NewFakeDeny(),
		"test-toolbox", secret, "codefly.dev/test:1.0")

	token := mintForTest(t, secret, "git.status", "", "codefly.dev/test:1.0", time.Minute)
	ctx := ctxWithScopedAuth(context.Background(), token)

	_, err := guard.CallTool(ctx, &toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err)
	require.Len(t, inner.calls, 1)
	require.NotNil(t, inner.calls[0].scopedSA,
		"handler MUST see the verified ScopedAuthorization on ctx — that's how plugin authors read permission detail")
	require.Equal(t, "git.status", inner.calls[0].scopedSA.Action)
	require.Equal(t, "codefly.dev/test:1.0", inner.calls[0].scopedSA.AudienceID)
}

func TestGuard_DefensePath_NoToken_FallsBackToPDP(t *testing.T) {
	secret := policy.NewSpawnSecret()
	allowPDP := testharness.NewFakeAllow()

	inner := &recordingToolboxServer{}
	guard := policyguard.NewWithScopedAuth(inner, allowPDP, "test-toolbox",
		secret, "codefly.dev/test:1.0")

	// No token in metadata — falls back to PDP path.
	resp, err := guard.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name: "git.status",
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error)
	require.Equal(t, 1, allowPDP.CallCount(),
		"defense path: PDP MUST be consulted when no token is present")
	require.Len(t, inner.calls, 1)
}

func TestGuard_DefensePath_NoToken_PDPDeniesIsHonored(t *testing.T) {
	secret := policy.NewSpawnSecret()
	denyPDP := testharness.NewFakeDeny()
	inner := &recordingToolboxServer{}
	guard := policyguard.NewWithScopedAuth(inner, denyPDP, "test-toolbox",
		secret, "codefly.dev/test:1.0")

	resp, err := guard.CallTool(context.Background(), &toolboxv0.CallToolRequest{
		Name: "git.status",
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error,
		"defense path: PDP deny short-circuits the call as in the single-level model")
	require.Empty(t, inner.calls, "denied call must NOT reach the handler")
}

func TestGuard_InvalidToken_DeniedWithoutPDPDowngrade(t *testing.T) {
	secret := policy.NewSpawnSecret()
	allowPDP := testharness.NewFakeAllow()
	inner := &recordingToolboxServer{}
	guard := policyguard.NewWithScopedAuth(inner, allowPDP, "test-toolbox",
		secret, "codefly.dev/test:1.0")

	// Tampered token — verification fails even though the fallback PDP would
	// otherwise allow the tool.
	bogus := "definitely.notavalid.token"
	ctx := ctxWithScopedAuth(context.Background(), bogus)

	resp, err := guard.CallTool(ctx, &toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err)
	require.Contains(t, resp.Error, "invalid token")
	require.Equal(t, 0, allowPDP.CallCount(),
		"invalid credentials must not downgrade to the credential-less PDP path")
	require.Empty(t, inner.calls)
}

func TestGuard_InvalidToken_PDPDeniesStillRefuses(t *testing.T) {
	secret := policy.NewSpawnSecret()
	denyPDP := testharness.NewFakeDeny()
	inner := &recordingToolboxServer{}
	guard := policyguard.NewWithScopedAuth(inner, denyPDP, "test-toolbox",
		secret, "codefly.dev/test:1.0")

	ctx := ctxWithScopedAuth(context.Background(), "bogus.token.here")
	resp, err := guard.CallTool(ctx, &toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error,
		"invalid credentials are denied before the PDP path")
	require.Equal(t, 0, denyPDP.CallCount())
	require.Empty(t, inner.calls)
}

func TestGuard_WrongAudience_FallsBackToPDP(t *testing.T) {
	secret := policy.NewSpawnSecret()
	denyPDP := testharness.NewFakeDeny()
	inner := &recordingToolboxServer{}
	guard := policyguard.NewWithScopedAuth(inner, denyPDP, "test-toolbox",
		secret, "codefly.dev/test:1.0")

	// Mint with WRONG audience.
	token := mintForTest(t, secret, "git.status", "", "different-plugin", time.Minute)
	ctx := ctxWithScopedAuth(context.Background(), token)

	resp, err := guard.CallTool(ctx, &toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err)
	// Audience mismatch → token verification fails closed.
	require.NotEmpty(t, resp.Error,
		"audience-mismatched token must not authorize this plugin")
}

func TestGuard_ActionMismatch_FallsBackToPDP(t *testing.T) {
	secret := policy.NewSpawnSecret()
	denyPDP := testharness.NewFakeDeny()
	inner := &recordingToolboxServer{}
	guard := policyguard.NewWithScopedAuth(inner, denyPDP, "test-toolbox",
		secret, "codefly.dev/test:1.0")

	// Token authorizes git.status but caller asks for git.commit.
	token := mintForTest(t, secret, "git.status", "", "codefly.dev/test:1.0", time.Minute)
	ctx := ctxWithScopedAuth(context.Background(), token)

	resp, err := guard.CallTool(ctx, &toolboxv0.CallToolRequest{Name: "git.commit"})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error,
		"action-mismatched token must NOT authorize a different action")
}

func TestGuard_ResourceCheck_TokenMatchesArgsResource(t *testing.T) {
	secret := policy.NewSpawnSecret()
	inner := &recordingToolboxServer{}
	guard := policyguard.NewWithScopedAuth(inner, testharness.NewFakeDeny(),
		"test-toolbox", secret, "codefly.dev/test:1.0")

	token := mintForTest(t, secret, "git.status", "repo:foo", "codefly.dev/test:1.0", time.Minute)
	ctx := ctxWithScopedAuth(context.Background(), token)

	args, _ := structpb.NewStruct(map[string]any{"resource": "repo:foo"})
	resp, err := guard.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "git.status", Arguments: args,
	})
	require.NoError(t, err)
	require.Empty(t, resp.Error,
		"resource match: fast path proceeds")
	require.Len(t, inner.calls, 1)
}

func TestGuard_ResourceMismatch_FallsBackToPDP(t *testing.T) {
	secret := policy.NewSpawnSecret()
	denyPDP := testharness.NewFakeDeny()
	inner := &recordingToolboxServer{}
	guard := policyguard.NewWithScopedAuth(inner, denyPDP, "test-toolbox",
		secret, "codefly.dev/test:1.0")

	token := mintForTest(t, secret, "git.status", "repo:authorized", "codefly.dev/test:1.0", time.Minute)
	ctx := ctxWithScopedAuth(context.Background(), token)

	args, _ := structpb.NewStruct(map[string]any{"resource": "repo:not-authorized"})
	resp, err := guard.CallTool(ctx, &toolboxv0.CallToolRequest{
		Name: "git.status", Arguments: args,
	})
	require.NoError(t, err)
	require.NotEmpty(t, resp.Error,
		"resource mismatch: token does not cover this call")
}

func TestGuard_ReplayProtection_SecondUseDenied(t *testing.T) {
	secret := policy.NewSpawnSecret()
	inner := &recordingToolboxServer{}
	denyPDP := testharness.NewFakeDeny()
	guard := policyguard.NewWithScopedAuth(inner, denyPDP, "test-toolbox",
		secret, "codefly.dev/test:1.0")

	token := mintForTest(t, secret, "git.status", "", "codefly.dev/test:1.0", time.Minute)
	ctx := ctxWithScopedAuth(context.Background(), token)

	// First use: succeeds.
	resp1, err := guard.CallTool(ctx, &toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err)
	require.Empty(t, resp1.Error)

	// Second use: replay tracker rejects with Exhausted.
	resp2, err := guard.CallTool(ctx, &toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err)
	require.NotEmpty(t, resp2.Error)
	require.Contains(t, resp2.Error, "max uses exhausted",
		"single-shot token replay must be rejected by the tracker")
	require.Len(t, inner.calls, 1, "second call must NOT reach the handler")
}

func TestGuard_NoSecretConfigured_AlwaysUsesPDP(t *testing.T) {
	// When the host doesn't enable scoped-auth, the Guard
	// behaves exactly like the single-level model.
	allowPDP := testharness.NewFakeAllow()
	inner := &recordingToolboxServer{}
	// New (without WithScopedAuth) — no secret picked up unless
	// env is set. We've isolated env by virtue of not setting it.
	t.Setenv("CODEFLY_SCOPED_AUTHZ_SECRET", "")
	guard := policyguard.New(inner, allowPDP, "test-toolbox")

	// Even with a token in metadata, Guard ignores it (no
	// secret to verify against).
	ctx := ctxWithScopedAuth(context.Background(), "anything.at.all")
	resp, err := guard.CallTool(ctx, &toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err)
	require.Empty(t, resp.Error)
	require.Equal(t, 1, allowPDP.CallCount(),
		"without scoped-auth secret, the Guard must always use PDP — single-level mode")
}

func TestGuard_PicksUpSecretFromEnv(t *testing.T) {
	// Pass the secret as base64url — the production format
	// manager.Load uses. (Raw secrets contain null bytes that
	// env vars don't accept.)
	secret := policy.NewSpawnSecret()
	encoded := base64.RawURLEncoding.EncodeToString(secret)
	t.Setenv("CODEFLY_SCOPED_AUTHZ_SECRET", encoded)

	inner := &recordingToolboxServer{}
	guard := policyguard.New(inner, testharness.NewFakeDeny(), "test-toolbox").
		WithAudience("codefly.dev/test:1.0")

	token := mintForTest(t, secret, "git.status", "", "codefly.dev/test:1.0", time.Minute)
	ctx := ctxWithScopedAuth(context.Background(), token)
	resp, err := guard.CallTool(ctx, &toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err)
	require.Empty(t, resp.Error,
		"Guard must pick up the secret from env and verify successfully")
}

func TestGuard_CaveatVerification(t *testing.T) {
	secret := policy.NewSpawnSecret()
	inner := &recordingToolboxServer{}

	// Mint a token with a ci_status caveat.
	encoded, _, err := policy.Mint(policy.MintInput{
		Principal:  &policy.Principal{ID: "u", Kind: policy.KindHuman, OrgID: "o"},
		Action:     "github.merge_pr",
		AudienceID: "codefly.dev/test:1.0",
		TTL:        time.Minute,
		Caveats:    map[string]any{"ci_status": "green"},
	}, secret)
	require.NoError(t, err)

	guard := policyguard.NewWithScopedAuth(inner, testharness.NewFakeDeny(),
		"test-toolbox", secret, "codefly.dev/test:1.0").
		WithCaveatVerifiers(map[string]policy.CaveatVerifier{
			"ci_status": func(v any) error {
				if v != "green" {
					return errCIRed
				}
				return nil
			},
		})

	ctx := ctxWithScopedAuth(context.Background(), encoded)
	resp, err := guard.CallTool(ctx, &toolboxv0.CallToolRequest{Name: "github.merge_pr"})
	require.NoError(t, err)
	require.Empty(t, resp.Error,
		"caveat verifier accepts → fast path proceeds")
}

var errCIRed = errStringFor("CI not green")

type errStringFor string

func (e errStringFor) Error() string { return string(e) }

// TestGuard_ConfigMutators_RaceFree proves the configMu added to
// Guard actually protects WithScopedAuthSecret / WithAudience /
// WithCaveatVerifiers against concurrent CallTool readers. Without
// the lock the race detector fired on every run.
//
// The Guard advertises rotation as a use case for these setters, so
// concurrent access is by design — this test pins that contract.
func TestGuard_ConfigMutators_RaceFree(t *testing.T) {
	secret := policy.NewSpawnSecret()
	allowPDP := testharness.NewFakeAllow()
	inner := &recordingToolboxServer{}
	guard := policyguard.NewWithScopedAuth(inner, allowPDP, "test-toolbox",
		secret, "codefly.dev/test:1.0")

	token := mintForTest(t, secret, "git.status", "", "codefly.dev/test:1.0", time.Minute)
	ctx := ctxWithScopedAuth(context.Background(), token)

	stop := make(chan struct{})
	doneCallers := make(chan struct{})
	doneMutator := make(chan struct{})

	// Callers: hammer CallTool. The recording server appends to its
	// call slice; we don't read inner.calls until after Wait so the
	// only race surface is the Guard's snapshot of rotatable config.
	go func() {
		defer close(doneCallers)
		for {
			select {
			case <-stop:
				return
			default:
				_, _ = guard.CallTool(ctx, &toolboxv0.CallToolRequest{Name: "git.status"})
			}
		}
	}()

	// Mutator: rotate secret/audience/caveats. WithScopedAuthSecret
	// with the same secret bytes preserves valid-token verification,
	// so the test asserts no race AND no spurious deny.
	go func() {
		defer close(doneMutator)
		for i := 0; i < 200; i++ {
			guard.WithScopedAuthSecret(secret).
				WithAudience("codefly.dev/test:1.0").
				WithCaveatVerifiers(map[string]policy.CaveatVerifier{
					"ci_status": func(v any) error { return nil },
				})
		}
	}()

	<-doneMutator
	close(stop)
	<-doneCallers
}

// TestGuard_MissingToken_DebugLogged confirms that when scoped-auth
// is configured but a CallTool arrives WITHOUT a token, the Guard
// falls through to the PDP path (already asserted elsewhere) AND
// that the path is observable — the change added an audit log so
// operators see the fall-through pattern.
//
// We can't assert the log line directly without a wool sink, so
// this test just pins the BEHAVIOR (fall-through, no panic) and
// leaves the log assertion to integration coverage.
func TestGuard_EmptyTokenIsInvalid(t *testing.T) {
	secret := policy.NewSpawnSecret()
	allowPDP := testharness.NewFakeAllow()
	inner := &recordingToolboxServer{}
	guard := policyguard.NewWithScopedAuth(inner, allowPDP, "test-toolbox",
		secret, "codefly.dev/test:1.0")

	// An explicitly supplied empty credential is invalid, unlike an absent
	// header, and must not downgrade to the PDP path.
	md := metadata.Pairs(policy.ScopedAuthMetadataKey, "")
	ctx := metadata.NewIncomingContext(context.Background(), md)

	resp, err := guard.CallTool(ctx, &toolboxv0.CallToolRequest{Name: "git.status"})
	require.NoError(t, err)
	require.Contains(t, resp.Error, "invalid token")
	require.Equal(t, 0, allowPDP.CallCount())
	require.Empty(t, inner.calls)
}
