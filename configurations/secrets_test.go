package configurations

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

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
		{"op://", false, "", ""},
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

func TestOnePasswordResolverClassifiesAuthenticationWithoutLeakingOutput(t *testing.T) {
	const canary = "PROVIDER_AUTH_OUTPUT_CANARY"
	r := NewOnePasswordResolver("")
	r.bin = writeStub(t, "op", "#!/bin/sh\nprintf 'you are not currently signed in "+canary+"' 1>&2\nexit 1\n")
	ref, ok := ParseSecretReference("op://dev-vault/item/field")
	require.True(t, ok)

	_, err := r.Resolve(context.Background(), ref)
	require.ErrorIs(t, err, ErrSecretProviderAuthenticationRequired)
	require.NotContains(t, err.Error(), canary)
	require.NotContains(t, err.Error(), ref.Raw)
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

type recordingSecretResolver struct {
	values map[string]string
	calls  map[string]int
}

func (r *recordingSecretResolver) Scheme() string { return OnePasswordScheme }

func (r *recordingSecretResolver) Resolve(_ context.Context, ref *SecretReference) (string, error) {
	if r.calls == nil {
		r.calls = make(map[string]int)
	}
	r.calls[ref.Raw]++
	value, ok := r.values[ref.Raw]
	if !ok {
		return "", fmt.Errorf("reference unavailable")
	}
	return value, nil
}

func writeSecretTestFile(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
}

func referenceOnlyWorkspace(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeSecretTestFile(t, root, "workspace.codefly.yaml", "name: reference-only-test\nlayout: flat\n")
	return root
}

func TestManagerResolvesReferenceOnlyManifestsAndCachesReferences(t *testing.T) {
	ctx := context.Background()
	root := referenceOnlyWorkspace(t)
	writeSecretTestFile(t, root, "configurations/local/auth.secret.ref.env", `CLIENT_SECRET=op://dev-vault/auth/client-secret
REPEATED_SECRET=op://dev-vault/auth/client-secret
`)
	writeSecretTestFile(t, root, "configurations/local/database.secret.ref.yaml", `credentials:
  password: op://dev-vault/database/password
  copies:
    - op://dev-vault/database/password
`)

	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	loader, err := NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)
	resolver := &recordingSecretResolver{values: map[string]string{
		"op://dev-vault/auth/client-secret": "resolved-auth-value",
		"op://dev-vault/database/password":  "resolved-database-value",
	}}
	manager, err := NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader).WithSecretResolver(resolver)
	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	confs, err := manager.GetWorkspaceConfigurations(ctx)
	require.NoError(t, err)
	auth, err := resources.FindWorkspaceConfiguration(ctx, confs, "auth")
	require.NoError(t, err)
	clientSecret, err := resources.GetConfigurationValue(ctx, auth, "auth", "client_secret")
	require.NoError(t, err)
	require.Equal(t, "resolved-auth-value", clientSecret)
	require.True(t, auth.Infos[0].ConfigurationValues[0].Secret)

	database, err := resources.FindWorkspaceConfiguration(ctx, confs, "database")
	require.NoError(t, err)
	info, err := resources.GetConfigurationInformation(ctx, database, "database")
	require.NoError(t, err)
	require.True(t, info.Data.Secret)
	var parsed map[string]any
	require.NoError(t, InformationUnmarshal(info, &parsed))
	credentials := parsed["credentials"].(map[string]any)
	require.Equal(t, "resolved-database-value", credentials["password"])
	require.Equal(t, []any{"resolved-database-value"}, credentials["copies"])

	require.Equal(t, 1, resolver.calls["op://dev-vault/auth/client-secret"])
	require.Equal(t, 1, resolver.calls["op://dev-vault/database/password"])
}

func TestReferenceOnlyManifestMissingBackendDoesNotExposeReference(t *testing.T) {
	ctx := context.Background()
	root := referenceOnlyWorkspace(t)
	const reference = "op://sensitive-vault/sensitive-item/sensitive-field"
	writeSecretTestFile(t, root, "configurations/local/auth.secret.ref.env", "TOKEN="+reference+"\n")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	loader, err := NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)
	manager, err := NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader)

	err = manager.Load(ctx, resources.LocalEnvironment())
	require.Error(t, err)
	require.Contains(t, err.Error(), "not configured")
	require.Contains(t, err.Error(), `configuration "auth" key "TOKEN"`)
	require.NotContains(t, err.Error(), reference)
}

func TestProviderFailureSuppressesProviderOutput(t *testing.T) {
	const canary = "RESOLVER_OUTPUT_SECRET_CANARY"
	r := NewOnePasswordResolver("")
	r.bin = writeStub(t, "op", "#!/bin/sh\nprintf '"+canary+"'\nprintf '"+canary+"' 1>&2\nexit 23\n")
	ref, ok := ParseSecretReference("op://dev-vault/item/field")
	require.True(t, ok)

	_, err := r.Resolve(context.Background(), ref)
	require.Error(t, err)
	require.Contains(t, err.Error(), "provider command")
	require.NotContains(t, err.Error(), canary)
	require.NotContains(t, err.Error(), ref.Raw)
}

type failingSecretResolver struct {
	cause error
}

func (resolver *failingSecretResolver) Scheme() string { return OnePasswordScheme }

func (resolver *failingSecretResolver) Resolve(context.Context, *SecretReference) (string, error) {
	return "", resolver.cause
}

func TestResolverErrorsAreWrappedWithoutLeakingValues(t *testing.T) {
	const canary = "RESOLVER_ERROR_SECRET_CANARY"
	cause := errors.New(canary)
	ctx := context.Background()
	root := referenceOnlyWorkspace(t)
	writeSecretTestFile(t, root, "configurations/local/auth.secret.ref.env", "TOKEN=op://dev-vault/auth/token\n")
	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	loader, err := NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)
	manager, err := NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader).WithSecretResolver(&failingSecretResolver{cause: cause})

	err = manager.Load(ctx, resources.LocalEnvironment())
	require.ErrorIs(t, err, cause)
	require.Contains(t, err.Error(), `secret provider "op" failed`)
	require.NotContains(t, err.Error(), canary)
}

func TestProviderResolutionCancellation(t *testing.T) {
	r := NewOnePasswordResolver("")
	r.bin = writeStub(t, "op", "#!/bin/sh\nwhile :; do :; done\n")
	ref, ok := ParseSecretReference("op://dev-vault/item/field")
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	started := time.Now()

	_, err := r.Resolve(ctx, ref)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Less(t, time.Since(started), 2*time.Second)
}

type secretLogCapture struct {
	logs []*wool.Log
}

func (capture *secretLogCapture) Process(log *wool.Log) {
	capture.logs = append(capture.logs, log)
}

func TestResolvedSecretsDoNotAppearInLogs(t *testing.T) {
	const canary = "RESOLVED_SECRET_LOG_CANARY"
	baseCtx := context.Background()
	capture := &secretLogCapture{}
	provider := wool.New(baseCtx, &wool.Resource{Kind: "test", Unique: "reference-only"}).WithLogger(capture)
	ctx := provider.Inject(baseCtx)
	previousLevel := wool.GlobalLogLevel()
	wool.SetGlobalLogLevel(wool.TRACE)
	t.Cleanup(func() { wool.SetGlobalLogLevel(previousLevel) })

	root := referenceOnlyWorkspace(t)
	writeSecretTestFile(t, root, "configurations/local/auth.secret.ref.env", "TOKEN=op://dev-vault/auth/token\n")
	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	loader, err := NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)
	manager, err := NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader).WithSecretResolver(&recordingSecretResolver{values: map[string]string{
		"op://dev-vault/auth/token": canary,
	}})
	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	for _, log := range capture.logs {
		require.NotContains(t, log.String(), canary)
	}
}

func TestRestrictedLoadDoesNotResolveUnselectedServiceSecrets(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	writeSecretTestFile(t, root, "workspace.codefly.yaml", `name: restricted-load-test
layout: flat
services:
  - name: selected
  - name: skipped
`)
	serviceFile := `kind: service
name: %s
version: 0.0.0
module: test
agent:
  kind: runtime::service
  name: go-grpc
  version: 0.0.1
  publisher: codefly.ai
`
	writeSecretTestFile(t, root, "services/selected/service.codefly.yaml", fmt.Sprintf(serviceFile, "selected"))
	writeSecretTestFile(t, root, "services/skipped/service.codefly.yaml", fmt.Sprintf(serviceFile, "skipped"))
	writeSecretTestFile(t, root, "services/selected/configurations/local/database.secret.ref.env", "PASSWORD=op://dev-vault/selected/password\n")
	writeSecretTestFile(t, root, "services/skipped/configurations/local/database.secret.ref.env", "PASSWORD=op://dev-vault/skipped/password\n")

	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	selected, err := workspace.FindUniqueServiceByName(ctx, "selected")
	require.NoError(t, err)
	identity, err := selected.Identity()
	require.NoError(t, err)
	loader, err := NewConfigurationLocalReader(ctx, workspace)
	require.NoError(t, err)
	resolver := &recordingSecretResolver{values: map[string]string{
		"op://dev-vault/selected/password": "selected-value",
	}}
	manager, err := NewManager(ctx, workspace)
	require.NoError(t, err)
	manager.WithLoader(loader).WithSecretResolver(resolver)
	require.NoError(t, manager.Restrict(ctx, []*resources.ServiceIdentity{identity}))
	require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))

	confs, err := manager.GetServiceConfigurations(ctx)
	require.NoError(t, err)
	require.Len(t, confs, 1)
	require.Equal(t, identity.Unique(), confs[0].Origin)
	require.Zero(t, resolver.calls["op://dev-vault/skipped/password"])
}

func TestSubsequentLoadPicksUpProviderRotationWithoutChangingManifest(t *testing.T) {
	ctx := context.Background()
	root := referenceOnlyWorkspace(t)
	manifest := filepath.Join(root, "configurations/local/auth.secret.ref.env")
	writeSecretTestFile(t, root, "configurations/local/auth.secret.ref.env", "TOKEN=op://dev-vault/auth/token\n")
	original, err := os.ReadFile(manifest)
	require.NoError(t, err)

	load := func(value string) string {
		workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
		require.NoError(t, err)
		loader, err := NewConfigurationLocalReader(ctx, workspace)
		require.NoError(t, err)
		manager, err := NewManager(ctx, workspace)
		require.NoError(t, err)
		manager.WithLoader(loader).WithSecretResolver(&recordingSecretResolver{values: map[string]string{
			"op://dev-vault/auth/token": value,
		}})
		require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))
		confs, err := manager.GetWorkspaceConfigurations(ctx)
		require.NoError(t, err)
		auth, err := resources.FindWorkspaceConfiguration(ctx, confs, "auth")
		require.NoError(t, err)
		resolved, err := resources.GetConfigurationValue(ctx, auth, "auth", "token")
		require.NoError(t, err)
		return resolved
	}

	require.Equal(t, "first-provider-value", load("first-provider-value"))
	require.Equal(t, "rotated-provider-value", load("rotated-provider-value"))
	after, err := os.ReadFile(manifest)
	require.NoError(t, err)
	require.Equal(t, original, after)
}

func TestConcurrentReferenceOnlyLoadsAreIsolated(t *testing.T) {
	for _, tc := range []struct {
		name  string
		value string
	}{
		{name: "worktree-a", value: "worktree-a-provider-value"},
		{name: "worktree-b", value: "worktree-b-provider-value"},
		{name: "worktree-c", value: "worktree-c-provider-value"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			root := referenceOnlyWorkspace(t)
			writeSecretTestFile(t, root, "configurations/local/auth.secret.ref.env", "TOKEN=op://dev-vault/auth/token\n")
			workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
			require.NoError(t, err)
			loader, err := NewConfigurationLocalReader(ctx, workspace)
			require.NoError(t, err)
			manager, err := NewManager(ctx, workspace)
			require.NoError(t, err)
			manager.WithLoader(loader).WithSecretResolver(&recordingSecretResolver{values: map[string]string{
				"op://dev-vault/auth/token": tc.value,
			}})
			require.NoError(t, manager.Load(ctx, resources.LocalEnvironment()))
			confs, err := manager.GetWorkspaceConfigurations(ctx)
			require.NoError(t, err)
			auth, err := resources.FindWorkspaceConfiguration(ctx, confs, "auth")
			require.NoError(t, err)
			resolved, err := resources.GetConfigurationValue(ctx, auth, "auth", "token")
			require.NoError(t, err)
			require.Equal(t, tc.value, resolved)
		})
	}
}

func TestSecretYAMLResolutionErrorsAreDeterministic(t *testing.T) {
	ctx := context.Background()
	root := referenceOnlyWorkspace(t)
	writeSecretTestFile(t, root, "configurations/local/auth.secret.ref.yaml", `z: op://dev-vault/item/z
a: op://dev-vault/item/a
`)
	workspace, err := resources.LoadWorkspaceFromDir(ctx, root)
	require.NoError(t, err)
	var first string
	for range 20 {
		loader, err := NewConfigurationLocalReader(ctx, workspace)
		require.NoError(t, err)
		manager, err := NewManager(ctx, workspace)
		require.NoError(t, err)
		manager.WithLoader(loader)
		err = manager.Load(ctx, resources.LocalEnvironment())
		require.Error(t, err)
		if first == "" {
			first = err.Error()
		}
		require.Equal(t, first, err.Error())
	}
	require.Contains(t, first, `$["a"]`)
}
