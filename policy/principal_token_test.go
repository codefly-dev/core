package policy_test

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/codefly-dev/core/policy"
)

func TestEncodeDecodePrincipalToken_Human_RoundTrip(t *testing.T) {
	p := &policy.Principal{
		ID:          "p-1",
		Kind:        policy.KindHuman,
		OrgID:       "", // humans cross-org
		DisplayName: "antoine",
		ExpiresAt:   time.Now().Add(time.Hour),
	}

	token, err := policy.EncodePrincipalToken(p)
	require.NoError(t, err)
	require.NotEmpty(t, token)
	require.False(t, strings.ContainsAny(token, "/+="),
		"token must use base64url (URL-safe), not standard base64")

	got, err := policy.DecodePrincipalToken(token)
	require.NoError(t, err)
	require.Equal(t, p.ID, got.ID)
	require.Equal(t, p.Kind, got.Kind)
	require.Equal(t, p.DisplayName, got.DisplayName)
	require.Equal(t, token, got.Token, "decoded principal must carry the original token")
}

func TestEncodeDecodePrincipalToken_Agent_PreservesAgentID(t *testing.T) {
	p := &policy.Principal{
		ID:          "p-2",
		Kind:        policy.KindAgent,
		OrgID:       "org-1",
		AgentID:     "codefly.dev/auto-merge:0.1.0",
		DisplayName: "Auto Merge Bot",
		ExpiresAt:   time.Now().Add(30 * time.Minute),
	}
	token, err := policy.EncodePrincipalToken(p)
	require.NoError(t, err)
	got, err := policy.DecodePrincipalToken(token)
	require.NoError(t, err)
	require.Equal(t, "codefly.dev/auto-merge:0.1.0", got.AgentID)
	require.Equal(t, "org-1", got.OrgID)
}

func TestEncodeDecodePrincipalToken_DelegationChain_Preserved(t *testing.T) {
	p := &policy.Principal{
		ID:      "p-3",
		Kind:    policy.KindAgent,
		OrgID:   "org-1",
		AgentID: "codefly.dev/sub-agent:0.1.0",
		DelegationChain: []policy.DelegationLink{
			{PrincipalID: "u-antoine", Kind: policy.KindHuman, DisplayName: "antoine", GrantID: "g-100"},
			{PrincipalID: "a-mind", Kind: policy.KindAgent, DisplayName: "Mind", GrantID: "g-101"},
		},
		ExpiresAt: time.Now().Add(time.Hour),
	}
	token, err := policy.EncodePrincipalToken(p)
	require.NoError(t, err)
	got, err := policy.DecodePrincipalToken(token)
	require.NoError(t, err)
	require.Len(t, got.DelegationChain, 2)
	require.Equal(t, "u-antoine", got.DelegationChain[0].PrincipalID)
	require.Equal(t, "g-100", got.DelegationChain[0].GrantID)
	require.Equal(t, "a-mind", got.DelegationChain[1].PrincipalID)
}

func TestEncodePrincipalToken_NilPrincipal_Errors(t *testing.T) {
	_, err := policy.EncodePrincipalToken(nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "nil")
}

func TestDecodePrincipalToken_Empty_Errors(t *testing.T) {
	_, err := policy.DecodePrincipalToken("")
	require.Error(t, err)
}

func TestDecodePrincipalToken_BadBase64_Errors(t *testing.T) {
	_, err := policy.DecodePrincipalToken("not!base64!at!all")
	require.Error(t, err)
	require.Contains(t, err.Error(), "base64")
}

func TestDecodePrincipalToken_BadJSON_Errors(t *testing.T) {
	// base64url-encoded "not json"
	_, err := policy.DecodePrincipalToken("bm90IGpzb24")
	require.Error(t, err)
}

func TestDecodePrincipalToken_Expired_Errors(t *testing.T) {
	p := &policy.Principal{
		ID:          "p",
		Kind:        policy.KindHuman,
		DisplayName: "x",
		ExpiresAt:   time.Now().Add(-time.Second),
	}
	token, err := policy.EncodePrincipalToken(p)
	require.NoError(t, err)

	_, err = policy.DecodePrincipalToken(token)
	require.Error(t, err, "expired tokens must NOT decode (defense at boundary)")
	require.Contains(t, err.Error(), "expired")
}

func TestDecodePrincipalToken_UnknownFormat_Errors(t *testing.T) {
	// Hand-build a token with an unknown format. JSON inline avoids
	// reaching for the unexported envelope type.
	rawJSON := []byte(`{"id":"p","kind":"human","display_name":"x","iat":1,"fmt":"v999-time-traveler"}`)
	token := base64.RawURLEncoding.EncodeToString(rawJSON)

	_, err := policy.DecodePrincipalToken(token)
	require.Error(t, err)
	require.Contains(t, err.Error(), "format")
}

func TestDecodePrincipalToken_InvalidPrincipal_Errors(t *testing.T) {
	// Invalid principal: agent without agent_id.
	rawJSON := []byte(`{"id":"p","kind":"agent","org_id":"o","iat":1,"exp":99999999999,"fmt":"v1-unsigned"}`)
	token := base64.RawURLEncoding.EncodeToString(rawJSON)

	_, err := policy.DecodePrincipalToken(token)
	require.Error(t, err, "invalid principal envelope must fail decode")
}
