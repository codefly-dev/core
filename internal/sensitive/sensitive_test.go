package sensitive_test

import (
	"testing"

	"github.com/codefly-dev/core/internal/sensitive"
	"github.com/stretchr/testify/require"
)

func TestRedactTextProtectsHumanAndMachineSecretLabels(t *testing.T) {
	const unseal = "unseal-value-must-not-survive"
	const token = "root-token-must-not-survive"
	input := "Unseal Key: " + unseal + "\nROOT_TOKEN=" + token + "\nApi Address: http://127.0.0.1:8200\n"

	redacted := sensitive.RedactText(input)
	require.NotContains(t, redacted, unseal)
	require.NotContains(t, redacted, token)
	require.Contains(t, redacted, "Unseal Key: ****")
	require.Contains(t, redacted, "ROOT_TOKEN=****")
	require.Contains(t, redacted, "Api Address: http://127.0.0.1:8200")
}

func TestKeyCanonicalizesHumanReadableSeparators(t *testing.T) {
	for _, key := range []string{"unseal key", "api-key", "private.key", "database/url", "auth token"} {
		require.Truef(t, sensitive.Key(key), "key %q was not recognized", key)
	}
	require.False(t, sensitive.Key("api address"))
}

// AUTH is matched per word, not as a substring. Both sides are listed so a
// narrowing that lets a credential through fails here, as does a regression
// that pushes a public identity value back under secret configuration.
func TestKeyAuthWordsSeparateCredentialsFromPublicIdentityValues(t *testing.T) {
	secret := []string{
		// A bare or compound AUTH word names credential material.
		"AUTH",
		"BASIC_AUTH",
		"PROXY_AUTH",
		"X_AUTH",
		"AUTH_HEADER",
		"AUTH_URL",
		"OAUTH",
		"OAUTH_CLIENT_ID",
		"NEXTAUTH_URL",
		"TS_AUTHKEY",
		"AUTHN",
		"AUTHZ",
		// The header that carries a bearer credential, in any spelling.
		"AUTHORIZATION",
		"HTTP_AUTHORIZATION",
		"Authorization",
		"authorization-header",
		"AUTHENTICATION",
		"SMTP_AUTHENTICATION",
		"AUTHORIZATION_URL",
		// A public AUTH word does not declassify a credential noun.
		"CERTIFICATE_AUTHORITY_KEY",
		"AUTHORITY_SIGNING_KEY",
		"AUTHORITY_KEYS",
		"AUTHORITY_SEED",
		"AUTHORITY_PASSPHRASE",
		"AUTHORITY_HMAC",
		"AUTHORITY_PEM",
		// Unseparated spellings are never inferred to be public.
		"AUTHORITYISSUER",
		"authorizeUrl",
		// Every other marker still matches as a substring, AUTH or not.
		"AUTH_TOKEN",
		"AUTHORITY_CLIENT_SECRET",
		"AUTHORITY_TOKEN_URL",
		"IDENTITY_TOKEN_URL",
		"AUTHORITY_PASSWORD",
		"AUTHORITY_API_KEY",
		"AUTHORITY_PRIVATE_KEY",
		"AUTHORITY_CREDENTIALS",
		"AUTHORITY_SESSION",
		"AUTHORITY_COOKIE",
		"AUTHORITY_DSN",
		"AUTHORITY_CONNECTION",
		"AUTHORITY_DATABASE_URL",
		"CODEFLY__SERVICE_SECRET_CONFIGURATION__APP__SVC__IDENTITY__AUTHORITY_ISSUER",
	}
	for _, key := range secret {
		require.Truef(t, sensitive.Key(key), "credential key %q must stay sensitive", key)
	}

	public := []string{
		"AUTHORITY_ISSUER",
		"AUTHORITY_AUDIENCE",
		"AUTHORITY",
		"AUTHORITIES",
		"IDENTITY_AUTHORIZE_URL",
		"IDENTITY_AUTHORIZE_SELECTOR",
		"AUTHORITY_JWKS_URL",
		"CODEFLY__SERVICE_CONFIGURATION__APP__SVC__IDENTITY__AUTHORITY_ISSUER",
		"authority issuer",
		"author",
		"AUTHORS",
		"api address",
	}
	for _, key := range public {
		require.Falsef(t, sensitive.Key(key), "public key %q must not be classified as a credential", key)
	}
}

func TestRedactTextKeepsPublicIdentityValuesAndRedactsAuthorization(t *testing.T) {
	const bearer = "Bearer must-not-survive"
	input := "AUTHORITY_ISSUER=https://issuer.example.com\nAuthorization: " + bearer + "\n"
	redacted := sensitive.RedactText(input)
	require.Contains(t, redacted, "AUTHORITY_ISSUER=https://issuer.example.com")
	require.NotContains(t, redacted, bearer)
	require.Contains(t, redacted, "Authorization: ****")
}
