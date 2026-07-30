package credentials_test

import (
	"net/http"
	"testing"
	"time"

	providerv0 "github.com/codefly-dev/core/generated/go/codefly/services/provider/v0"
	"github.com/codefly-dev/core/network/urlguard"
	"github.com/codefly-dev/core/provider/credentials"
	"github.com/stretchr/testify/require"
)

const secret = "sk_live_1234567890abcdef"

func binding() *providerv0.BindingAddress {
	return &providerv0.BindingAddress{WorkspaceId: "ws", EnvironmentId: "env", BindingId: "bind"}
}

func origin() urlguard.Origin {
	return urlguard.Origin{Scheme: "https", Host: "api.stripe.com", Port: 443}
}

func baseScope() credentials.Scope {
	return credentials.Scope{
		Principal:      "user",
		Organization:   "org",
		ArtifactDigest: "sha256:aa",
		Binding:        binding(),
		PlanID:         "plan-1",
		ActionID:       "action-1",
		RequestDigest:  "sha256:req",
		Purpose:        providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT,
		Origin:         origin(),
		Method:         providerv0.HTTPMethod_HTTP_METHOD_POST,
		Injection:      credentials.Injection{Kind: credentials.InjectBearer},
		MaxUses:        1,
		TTL:            time.Minute,
	}
}

func baseUse() credentials.Use {
	return credentials.Use{
		Purpose:       providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT,
		Binding:       binding(),
		ActionID:      "action-1",
		RequestDigest: "sha256:req",
		Origin:        origin(),
		Method:        providerv0.HTTPMethod_HTTP_METHOD_POST,
	}
}

func newRequest(t *testing.T) *http.Request {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, "https://api.stripe.com/v1/x", nil)
	require.NoError(t, err)
	return request
}

func TestMint_ReturnsOpaqueHandleNoSecret(t *testing.T) {
	vault := credentials.NewVault()
	handle, err := vault.Mint(secret, baseScope())
	require.NoError(t, err)
	require.NotContains(t, handle.GetHandle(), secret)
	require.Equal(t, providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_MANAGEMENT, handle.GetPurpose())
}

func TestInject_SucceedsThenExhaustsUses(t *testing.T) {
	vault := credentials.NewVault()
	handle, err := vault.Mint(secret, baseScope())
	require.NoError(t, err)

	request := newRequest(t)
	require.NoError(t, vault.Inject(request, handle.GetHandle(), baseUse()))
	require.Equal(t, "Bearer "+secret, request.Header.Get("Authorization"))

	// Single-use handle is now spent.
	err = vault.Inject(newRequest(t), handle.GetHandle(), baseUse())
	require.Error(t, err)
}

func TestInject_CrossPurposeRejected(t *testing.T) {
	vault := credentials.NewVault()
	handle, err := vault.Mint(secret, baseScope())
	require.NoError(t, err)

	use := baseUse()
	use.Purpose = providerv0.CredentialPurpose_CREDENTIAL_PURPOSE_RUNTIME
	request := newRequest(t)
	require.ErrorContains(t, vault.Inject(request, handle.GetHandle(), use), "purpose")
	require.Empty(t, request.Header.Get("Authorization"))
}

func TestInject_CrossBindingRejected(t *testing.T) {
	vault := credentials.NewVault()
	handle, err := vault.Mint(secret, baseScope())
	require.NoError(t, err)

	use := baseUse()
	use.Binding = &providerv0.BindingAddress{WorkspaceId: "ws", EnvironmentId: "env", BindingId: "other"}
	require.ErrorContains(t, vault.Inject(newRequest(t), handle.GetHandle(), use), "binding")
}

func TestInject_CrossActionRejected(t *testing.T) {
	vault := credentials.NewVault()
	handle, err := vault.Mint(secret, baseScope())
	require.NoError(t, err)

	use := baseUse()
	use.ActionID = "action-2"
	require.ErrorContains(t, vault.Inject(newRequest(t), handle.GetHandle(), use), "action")
}

func TestInject_WrongOriginOrMethodRejected(t *testing.T) {
	vault := credentials.NewVault()
	handle, err := vault.Mint(secret, baseScope())
	require.NoError(t, err)

	wrongOrigin := baseUse()
	wrongOrigin.Origin = urlguard.Origin{Scheme: "https", Host: "evil.com", Port: 443}
	require.ErrorContains(t, vault.Inject(newRequest(t), handle.GetHandle(), wrongOrigin), "origin")

	wrongMethod := baseUse()
	wrongMethod.Method = providerv0.HTTPMethod_HTTP_METHOD_DELETE
	require.ErrorContains(t, vault.Inject(newRequest(t), handle.GetHandle(), wrongMethod), "method")
}

func TestInject_ExpiredRejected(t *testing.T) {
	current := time.Unix(1000, 0)
	vault := credentials.NewVault().WithClock(func() time.Time { return current })
	scope := baseScope()
	scope.TTL = 30 * time.Second
	handle, err := vault.Mint(secret, scope)
	require.NoError(t, err)

	current = current.Add(time.Minute)
	require.ErrorContains(t, vault.Inject(newRequest(t), handle.GetHandle(), baseUse()), "expired")
}

func TestInject_HeaderAndQueryInjection(t *testing.T) {
	vault := credentials.NewVault()
	headerScope := baseScope()
	headerScope.Injection = credentials.Injection{Kind: credentials.InjectHeader, Name: "X-Api-Key"}
	headerHandle, err := vault.Mint(secret, headerScope)
	require.NoError(t, err)
	request := newRequest(t)
	require.NoError(t, vault.Inject(request, headerHandle.GetHandle(), baseUse()))
	require.Equal(t, secret, request.Header.Get("X-Api-Key"))

	queryScope := baseScope()
	queryScope.Injection = credentials.Injection{Kind: credentials.InjectQuery, Name: "token"}
	queryHandle, err := vault.Mint(secret, queryScope)
	require.NoError(t, err)
	request = newRequest(t)
	require.NoError(t, vault.Inject(request, queryHandle.GetHandle(), baseUse()))
	require.Equal(t, secret, request.URL.Query().Get("token"))
}

func TestMint_RejectsIncompleteScope(t *testing.T) {
	vault := credentials.NewVault()
	scope := baseScope()
	scope.ActionID = ""
	_, err := vault.Mint(secret, scope)
	require.Error(t, err)
}
