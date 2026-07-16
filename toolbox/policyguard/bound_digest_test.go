package policyguard_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/policy"
	"github.com/codefly-dev/core/policy/testharness"
	"github.com/codefly-dev/core/toolbox"
	"github.com/codefly-dev/core/toolbox/conformance"
	"github.com/codefly-dev/core/toolbox/policyguard"
)

func boundRequest(t *testing.T, subject string) *toolboxv0.CallToolRequest {
	t.Helper()
	arguments, err := structpb.NewStruct(map[string]any{"subject": subject})
	require.NoError(t, err)
	return &toolboxv0.CallToolRequest{Name: conformance.IdentityTool, Arguments: arguments}
}

func boundToken(t *testing.T, secret []byte, request *toolboxv0.CallToolRequest, catalogDigest, requestDigest string) string {
	t.Helper()
	token, _, err := policy.Mint(policy.MintInput{
		Principal:     &policy.Principal{ID: "user-1", Kind: policy.KindHuman, OrgID: "org-1"},
		Action:        request.GetName(),
		AudienceID:    "codefly.dev/conformance-fixture:0.0.1",
		CatalogDigest: catalogDigest,
		RequestDigest: requestDigest,
		TTL:           time.Minute,
	}, secret)
	require.NoError(t, err)
	return token
}

func approvedCatalogDigest(t *testing.T, server toolboxv0.ToolboxServer, request *toolboxv0.CallToolRequest) string {
	t.Helper()
	snapshot, err := toolbox.SnapshotServer(context.Background(), server)
	require.NoError(t, err)
	description, err := server.DescribeTool(context.Background(), &toolboxv0.DescribeToolRequest{Name: request.GetName()})
	require.NoError(t, err)
	approved, err := snapshot.ApproveTool(request.GetName(), description)
	require.NoError(t, err)
	return approved.Digest
}

func callWithToken(t *testing.T, guard *policyguard.Guard, request *toolboxv0.CallToolRequest, token string) *toolboxv0.CallToolResponse {
	t.Helper()
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(policy.ScopedAuthMetadataKey, token))
	response, err := guard.CallTool(ctx, request)
	require.NoError(t, err)
	return response
}

func TestGuardRequiredBindingsAcceptExactCatalogAndRequest(t *testing.T) {
	secret := policy.NewSpawnSecret()
	server := conformance.New(conformance.FixtureVersion)
	request := boundRequest(t, "one")
	requestDigest, err := toolbox.DigestCallToolRequest(request)
	require.NoError(t, err)
	guard := policyguard.NewWithScopedAuth(server, testharness.NewFakeDeny(),
		conformance.FixtureName, secret, "codefly.dev/conformance-fixture:0.0.1").
		WithRequiredAuthorizationBindings()

	response := callWithToken(t, guard, request,
		boundToken(t, secret, request, approvedCatalogDigest(t, server, request), requestDigest))
	require.Empty(t, response.Error)
}

func TestGuardRequiredBindingsRejectMissingWrongOrReusedDigests(t *testing.T) {
	secret := policy.NewSpawnSecret()
	server := conformance.New(conformance.FixtureVersion)
	approved := boundRequest(t, "one")
	approvedDigest, err := toolbox.DigestCallToolRequest(approved)
	require.NoError(t, err)
	guard := policyguard.NewWithScopedAuth(server, testharness.NewFakeDeny(),
		conformance.FixtureName, secret, "codefly.dev/conformance-fixture:0.0.1").
		WithRequiredAuthorizationBindings()

	missing := callWithToken(t, guard, approved, boundToken(t, secret, approved, "", ""))
	require.Equal(t, "scoped-authz: invalid token", missing.Error)

	wrongCatalog := callWithToken(t, guard, approved,
		boundToken(t, secret, approved, "sha256:wrong", approvedDigest))
	require.Equal(t, "scoped-authz: invalid token", wrongCatalog.Error)

	tampered := boundRequest(t, "two")
	reused := callWithToken(t, guard, tampered,
		boundToken(t, secret, approved, approvedCatalogDigest(t, server, approved), approvedDigest))
	require.Equal(t, "scoped-authz: invalid token", reused.Error)
}

func TestGuardRequiredBindingsRejectAbsentTokenWithoutPDPFallback(t *testing.T) {
	secret := policy.NewSpawnSecret()
	server := conformance.New(conformance.FixtureVersion)
	allow := testharness.NewFakeAllow()
	guard := policyguard.NewWithScopedAuth(server, allow,
		conformance.FixtureName, secret, "codefly.dev/conformance-fixture:0.0.1").
		WithRequiredAuthorizationBindings()

	response, err := guard.CallTool(context.Background(), boundRequest(t, "no-token"))
	require.NoError(t, err)
	require.Equal(t, "scoped-authz: token required", response.Error)
	require.Zero(t, allow.CallCount(), "bound production calls must not downgrade to the unbound PDP path")
}

func TestGuardConsumesSingleUseTokenEvenWhenHandlerTimesOut(t *testing.T) {
	secret := policy.NewSpawnSecret()
	server := conformance.New(conformance.FixtureVersion)
	arguments, err := structpb.NewStruct(map[string]any{"duration_ms": 500})
	require.NoError(t, err)
	request := &toolboxv0.CallToolRequest{Name: conformance.WaitTool, Arguments: arguments}
	requestDigest, err := toolbox.DigestCallToolRequest(request)
	require.NoError(t, err)
	guard := policyguard.NewWithScopedAuth(server, testharness.NewFakeDeny(),
		conformance.FixtureName, secret, "codefly.dev/conformance-fixture:0.0.1").
		WithRequiredAuthorizationBindings()
	token := boundToken(t, secret, request, approvedCatalogDigest(t, server, request), requestDigest)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Millisecond)
	defer cancel()
	ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(policy.ScopedAuthMetadataKey, token))
	first, err := guard.CallTool(ctx, request)
	require.NoError(t, err)
	require.Contains(t, first.Error, "wait canceled")

	second := callWithToken(t, guard, request, token)
	require.Contains(t, second.Error, "max uses exhausted")
}

func TestGuardRejectsWrongTenantCaveat(t *testing.T) {
	secret := policy.NewSpawnSecret()
	server := conformance.New(conformance.FixtureVersion)
	request := boundRequest(t, "tenant-bound")
	requestDigest, err := toolbox.DigestCallToolRequest(request)
	require.NoError(t, err)
	catalogDigest := approvedCatalogDigest(t, server, request)
	guard := policyguard.NewWithScopedAuth(server, testharness.NewFakeDeny(),
		conformance.FixtureName, secret, "codefly.dev/conformance-fixture:0.0.1").
		WithRequiredAuthorizationBindings().
		WithWellKnownCaveatExpectations(policy.WellKnownCaveatExpectations{TenantID: "tenant-1"})

	mint := func(tenant string) string {
		caveats, mapErr := (policy.WellKnownCaveats{TenantID: tenant}).Map()
		require.NoError(t, mapErr)
		token, _, mintErr := policy.Mint(policy.MintInput{
			Principal: &policy.Principal{ID: "user-1", Kind: policy.KindHuman},
			Action:    request.GetName(), AudienceID: "codefly.dev/conformance-fixture:0.0.1",
			CatalogDigest: catalogDigest, RequestDigest: requestDigest,
			Caveats: caveats, TTL: time.Minute,
		}, secret)
		require.NoError(t, mintErr)
		return token
	}

	wrong := callWithToken(t, guard, request, mint("tenant-2"))
	require.Equal(t, "scoped-authz: invalid token", wrong.Error)
	correct := callWithToken(t, guard, request, mint("tenant-1"))
	require.Empty(t, correct.Error)
}

func TestGuardBindsNamedQueryAndResultBudgetCaveats(t *testing.T) {
	secret := policy.NewSpawnSecret()
	server := conformance.New(conformance.FixtureVersion)
	arguments, err := structpb.NewStruct(map[string]any{
		"query_id": "customer.lookup",
		"result_budget": map[string]any{
			"max_rows": 10, "max_bytes": 1000, "max_duration_ms": 500,
		},
	})
	require.NoError(t, err)
	request := &toolboxv0.CallToolRequest{Name: conformance.IdentityTool, Arguments: arguments}
	requestDigest, err := toolbox.DigestCallToolRequest(request)
	require.NoError(t, err)
	catalogDigest := approvedCatalogDigest(t, server, request)
	guard := policyguard.NewWithScopedAuth(server, testharness.NewFakeDeny(),
		conformance.FixtureName, secret, "codefly.dev/conformance-fixture:0.0.1").
		WithRequiredAuthorizationBindings()

	mint := func(queryID string, budget policy.ResultBudget) string {
		caveats, mapErr := (policy.WellKnownCaveats{
			QueryIDs: []string{queryID}, ResultBudget: &budget,
		}).Map()
		require.NoError(t, mapErr)
		token, _, mintErr := policy.Mint(policy.MintInput{
			Principal: &policy.Principal{ID: "user-1", Kind: policy.KindHuman},
			Action:    request.GetName(), AudienceID: "codefly.dev/conformance-fixture:0.0.1",
			CatalogDigest: catalogDigest, RequestDigest: requestDigest,
			Caveats: caveats, TTL: time.Minute,
		}, secret)
		require.NoError(t, mintErr)
		return token
	}

	wrongQuery := callWithToken(t, guard, request, mint("other.query", policy.ResultBudget{
		MaxRows: 100, MaxBytes: 10000, MaxDurationMillis: 1000,
	}))
	require.Equal(t, "scoped-authz: invalid token", wrongQuery.Error)

	tooSmall := callWithToken(t, guard, request, mint("customer.lookup", policy.ResultBudget{
		MaxRows: 5, MaxBytes: 10000, MaxDurationMillis: 1000,
	}))
	require.Equal(t, "scoped-authz: invalid token", tooSmall.Error)

	accepted := callWithToken(t, guard, request, mint("customer.lookup", policy.ResultBudget{
		MaxRows: 100, MaxBytes: 10000, MaxDurationMillis: 1000,
	}))
	require.NotContains(t, accepted.Error, "scoped-authz",
		"matching query/budget bindings must pass the Guard; fixture schema may reject its unrelated arguments later")
}
