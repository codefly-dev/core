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
		// A scheme with no registered backend is not a reference — passthrough.
		{"aws-sm://codefly/dev/auth0#client_secret", false, "", ""},
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

	manager, err := NewManager(ctx, ws)
	require.NoError(t, err)
	manager.WithLoader(loader).WithSecretResolver(op)

	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	confs, err := manager.GetWorkspaceConfigurations(ctx)
	require.NoError(t, err)

	// op reference resolved
	frontend, err := resources.FindWorkspaceConfiguration(ctx, confs, "auth0/frontend")
	require.NoError(t, err)
	v, err := resources.GetConfigurationValue(ctx, frontend, "auth0/frontend", "client_secret")
	require.NoError(t, err)
	require.Equal(t, "op-client-secret", v)

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
	require.Equal(t, "op-db-password", nested["api_token"])
	require.Equal(t, "keep-me", nested["plain"])
}

// End-to-end through the real selection path: the environment declared in
// workspace.codefly.yaml (`secrets:`) drives resolver construction, and that
// resolver uses the default `op` binary — here shadowed by a stub on PATH.
// No resolvers are injected.
func TestManagerResolvesViaEnvironmentProvider(t *testing.T) {
	ctx := context.Background()
	dir, err := shared.SolvePath("testdata/secrets")
	require.NoError(t, err)
	ws, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	env := ws.FindEnvironment("local")
	require.NotNil(t, env)
	require.Len(t, env.Secrets, 1)
	require.Equal(t, ProviderOnePassword, env.Secrets[0].Kind)

	stubDir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(stubDir, "op"), []byte(opStub), 0o755))
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
