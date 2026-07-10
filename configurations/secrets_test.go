package configurations

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
	"github.com/codefly-dev/core/wool"

	"github.com/stretchr/testify/require"
)

// opStub stands in for the `op` CLI: it prints the field value for known
// op:// references and fails (to stderr) for anything else.
const opStub = `#!/bin/sh
uri=""
for a in "$@"; do
  case "$a" in
    op://*) uri="$a" ;;
  esac
done
case "$uri" in
  op://dev-vault/auth0/client_secret) printf 'op-client-secret' ;;
  op://dev-vault/db/password) printf 'op-db-password' ;;
  op://dev-vault/tls/key) printf '%s\n' '-----BEGIN KEY-----' 'line1' 'line2' '-----END KEY-----' ;;
  *) echo "op: item not found: $uri" 1>&2; exit 1 ;;
esac
`

// awsStub stands in for the `aws` CLI: it prints the SecretString for known
// --secret-id values.
const awsStub = `#!/bin/sh
id=""
while [ $# -gt 0 ]; do
  case "$1" in
    --secret-id) id="$2"; shift 2 ;;
    *) shift ;;
  esac
done
case "$id" in
  codefly/dev/token) printf 'aws-token-value' ;;
  codefly/dev/auth0) printf '{"client_secret":"aws-client-secret","client_id":"abc"}' ;;
  *) echo "aws: secret not found: $id" 1>&2; exit 1 ;;
esac
`

func writeStub(t *testing.T, name, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.WriteFile(p, []byte(body), 0o755))
	return p
}

func TestParseSecretReference(t *testing.T) {
	tcs := []struct {
		in     string
		ok     bool
		scheme string
		path   string
	}{
		{"op://dev-vault/auth0/client_secret", true, "op", "dev-vault/auth0/client_secret"},
		{"aws-sm://codefly/dev/auth0#client_secret", true, "aws-sm", "codefly/dev/auth0#client_secret"},
		{"postgres://user:pass@host/db", false, "", ""},
		{"literal-plaintext", false, "", ""},
		{"", false, "", ""},
	}
	for _, tc := range tcs {
		ref, ok := ParseSecretReference(tc.in)
		require.Equal(t, tc.ok, ok, tc.in)
		if tc.ok {
			require.Equal(t, tc.scheme, ref.Scheme)
			require.Equal(t, tc.path, ref.Path)
			require.Equal(t, tc.in, ref.Raw)
		}
	}
}

func TestOnePasswordResolver(t *testing.T) {
	ctx := context.Background()
	r := NewOnePasswordResolver("")
	r.bin = writeStub(t, "op", opStub)

	ref, ok := ParseSecretReference("op://dev-vault/auth0/client_secret")
	require.True(t, ok)
	v, err := r.Resolve(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, "op-client-secret", v)

	ref, _ = ParseSecretReference("op://dev-vault/missing/x")
	_, err = r.Resolve(ctx, ref)
	require.Error(t, err)
}

// Only the single trailing newline the CLI appends is stripped; the secret's
// own internal newlines (a PEM key, say) survive intact.
func TestResolverPreservesMultilineSecret(t *testing.T) {
	ctx := context.Background()
	r := NewOnePasswordResolver("")
	r.bin = writeStub(t, "op", opStub)

	ref, _ := ParseSecretReference("op://dev-vault/tls/key")
	v, err := r.Resolve(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, "-----BEGIN KEY-----\nline1\nline2\n-----END KEY-----", v)
}

func TestAWSSecretsManagerResolver(t *testing.T) {
	ctx := context.Background()
	r := NewAWSSecretsManagerResolver("us-east-1")
	r.bin = writeStub(t, "aws", awsStub)

	ref, _ := ParseSecretReference("aws-sm://codefly/dev/token")
	v, err := r.Resolve(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, "aws-token-value", v)

	ref, _ = ParseSecretReference("aws-sm://codefly/dev/auth0#client_secret")
	v, err = r.Resolve(ctx, ref)
	require.NoError(t, err)
	require.Equal(t, "aws-client-secret", v)

	ref, _ = ParseSecretReference("aws-sm://codefly/dev/auth0#missing")
	_, err = r.Resolve(ctx, ref)
	require.Error(t, err)
}

func TestResolversFromEnvironment(t *testing.T) {
	rs, err := ResolversFromEnvironment(&resources.Environment{Name: "local"})
	require.NoError(t, err)
	require.Empty(t, rs)

	rs, err = ResolversFromEnvironment(&resources.Environment{
		Name:    "local",
		Secrets: []*resources.EnvironmentSecretProvider{{Kind: ProviderOnePassword}},
	})
	require.NoError(t, err)
	require.Len(t, rs, 1)
	require.Equal(t, OnePasswordScheme, rs[0].Scheme())

	// Multiple backends coexist in one environment.
	rs, err = ResolversFromEnvironment(&resources.Environment{
		Name: "prod",
		Secrets: []*resources.EnvironmentSecretProvider{
			{Kind: ProviderOnePassword},
			{Kind: ProviderAWSSecretsManager, Region: "us-east-1"},
		},
	})
	require.NoError(t, err)
	require.Len(t, rs, 2)
	require.Equal(t, OnePasswordScheme, rs[0].Scheme())
	require.Equal(t, AWSSecretsManagerScheme, rs[1].Scheme())

	_, err = ResolversFromEnvironment(&resources.Environment{
		Name:    "prod",
		Secrets: []*resources.EnvironmentSecretProvider{{Kind: "vault"}},
	})
	require.Error(t, err)
}

func TestManagerResolvesSecretReferences(t *testing.T) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	dir, err := shared.SolvePath("testdata/secrets")
	require.NoError(t, err)
	ws, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	loader, err := NewConfigurationLocalReader(ctx, ws)
	require.NoError(t, err)

	op := NewOnePasswordResolver("")
	op.bin = writeStub(t, "op", opStub)
	aws := NewAWSSecretsManagerResolver("")
	aws.bin = writeStub(t, "aws", awsStub)

	manager, err := NewManager(ctx, ws)
	require.NoError(t, err)
	manager.WithLoader(loader).WithSecretResolver(op, aws)

	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	confs, err := manager.GetWorkspaceConfigurations(ctx)
	require.NoError(t, err)

	// op reference resolved
	frontend, err := resources.FindWorkspaceConfiguration(ctx, confs, "auth0/frontend")
	require.NoError(t, err)
	v, err := resources.GetConfigurationValue(ctx, frontend, "auth0/frontend", "client_secret")
	require.NoError(t, err)
	require.Equal(t, "op-client-secret", v)

	// aws references resolved (whole SecretString + json-key extraction)
	awsConf, err := resources.FindWorkspaceConfiguration(ctx, confs, "aws")
	require.NoError(t, err)
	tok, err := resources.GetConfigurationValue(ctx, awsConf, "aws", "token")
	require.NoError(t, err)
	require.Equal(t, "aws-token-value", tok)
	dbp, err := resources.GetConfigurationValue(ctx, awsConf, "aws", "db_password")
	require.NoError(t, err)
	require.Equal(t, "aws-client-secret", dbp)

	// plaintext and unknown-scheme values pass through untouched
	plain, err := resources.FindWorkspaceConfiguration(ctx, confs, "plain")
	require.NoError(t, err)
	api, err := resources.GetConfigurationValue(ctx, plain, "plain", "api_key")
	require.NoError(t, err)
	require.Equal(t, "literal-plaintext-value", api)
	conn, err := resources.GetConfigurationValue(ctx, plain, "plain", "connection")
	require.NoError(t, err)
	require.Equal(t, "postgres://user:pass@localhost:5432/db", conn)

	// references embedded in a secret yaml blob are resolved in memory
	backend, err := resources.FindWorkspaceConfiguration(ctx, confs, "auth0/backend")
	require.NoError(t, err)
	info, err := resources.GetConfigurationInformation(ctx, backend, "auth0/backend")
	require.NoError(t, err)
	require.NotNil(t, info.Data)
	var parsed map[string]any
	require.NoError(t, InformationUnmarshal(info, &parsed))
	require.Equal(t, "op-client-secret", parsed["client_secret"])
	nested, ok := parsed["nested"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "aws-token-value", nested["api_token"])
	require.Equal(t, "keep-me", nested["plain"])
}

// End-to-end through the real selection path: the environment declared in
// workspace.codefly.yaml (`secrets:`) drives resolver construction, and those
// resolvers use the default `op`/`aws` binaries — here shadowed by stubs on
// PATH. No resolvers are injected.
func TestManagerResolvesViaEnvironmentProvider(t *testing.T) {
	ctx := context.Background()
	dir, err := shared.SolvePath("testdata/secrets")
	require.NoError(t, err)
	ws, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	// The workspace's "local" environment declares both backends.
	env := ws.FindEnvironment("local")
	require.NotNil(t, env)
	require.Len(t, env.Secrets, 2)
	require.Equal(t, ProviderOnePassword, env.Secrets[0].Kind)

	stubDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(stubDir, "op"), []byte(opStub), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stubDir, "aws"), []byte(awsStub), 0o755))
	t.Setenv("PATH", stubDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	loader, err := NewConfigurationLocalReader(ctx, ws)
	require.NoError(t, err)
	manager, err := NewManager(ctx, ws)
	require.NoError(t, err)
	manager.WithLoader(loader) // no injected resolvers — backend comes from the environment

	require.NoError(t, manager.Load(ctx, env))

	confs, err := manager.GetWorkspaceConfigurations(ctx)
	require.NoError(t, err)

	frontend, err := resources.FindWorkspaceConfiguration(ctx, confs, "auth0/frontend")
	require.NoError(t, err)
	v, err := resources.GetConfigurationValue(ctx, frontend, "auth0/frontend", "client_secret")
	require.NoError(t, err)
	require.Equal(t, "op-client-secret", v)

	awsConf, err := resources.FindWorkspaceConfiguration(ctx, confs, "aws")
	require.NoError(t, err)
	tok, err := resources.GetConfigurationValue(ctx, awsConf, "aws", "token")
	require.NoError(t, err)
	require.Equal(t, "aws-token-value", tok)

	// Service-level reference resolved through the same environment backend.
	sconfs, err := manager.GetServiceConfigurations(ctx)
	require.NoError(t, err)
	require.Len(t, sconfs, 1)
	pw, err := resources.GetConfigurationValue(ctx, sconfs[0], "db", "password")
	require.NoError(t, err)
	require.Equal(t, "op-db-password", pw)
}

// A reference whose backend is not configured for the environment must fail
// loudly rather than inject the raw op://… string as a value.
func TestManagerUnconfiguredBackendErrors(t *testing.T) {
	ctx := context.Background()
	dir, err := shared.SolvePath("testdata/secrets")
	require.NoError(t, err)
	ws, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	loader, err := NewConfigurationLocalReader(ctx, ws)
	require.NoError(t, err)

	manager, err := NewManager(ctx, ws)
	require.NoError(t, err)
	manager.WithLoader(loader)

	err = manager.Load(ctx, resources.LocalEnvironment())
	require.Error(t, err)
	require.Contains(t, err.Error(), "not configured")
}
