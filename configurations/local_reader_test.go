package configurations_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/codefly-dev/core/configurations"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/wool"

	"github.com/codefly-dev/core/shared"

	"github.com/stretchr/testify/require"
)

func TestLoadingDirectoryFromFilesFlat(t *testing.T) {
	testLoadConfigurationsFromFiles(t, "testdata/flat")
}

func TestLoadingDirectoryFromFilesModules(t *testing.T) {
	testLoadConfigurationsFromFiles(t, "testdata/module")
}

func testLoadConfigurationsFromFiles(t *testing.T, dir string) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	dir, err := shared.SolvePath(dir)
	require.NoError(t, err)
	ctx := context.Background()
	infos, err := configurations.LoadConfigurationInformationsFromFiles(ctx, dir)
	require.NoError(t, err)
	// workspace
	// auth0/frontend global other_global
	// service
	// nested/other something
	require.Len(t, infos, 5)
	// Some values

	for _, info := range infos {
		fmt.Println(info.Name, info.Data)
	}
	// Some data
	{
		info, err := resources.FilterConfigurationInformation(ctx, "configurations/local/other_global", infos...)
		require.NoError(t, err)
		require.NotNil(t, info)
		require.NotNil(t, info.Data)
		require.Equal(t, "yaml", info.Data.Kind)
		require.NotNil(t, info.Data.Content)
	}
}

func TestLocalLoaderFlatLayout(t *testing.T) {
	testLocalLoader(t, "testdata/flat")
}

func TestLocalLoaderModulesLayout(t *testing.T) {
	testLocalLoader(t, "testdata/module")
}

func testLocalLoader(t *testing.T, dir string) {
	wool.SetGlobalLogLevel(wool.DEBUG)
	ctx := context.Background()
	ws, err := resources.LoadWorkspaceFromDir(ctx, dir)
	require.NoError(t, err)

	loader, err := configurations.NewConfigurationLocalReader(ctx, ws)
	require.NoError(t, err)

	err = loader.Load(ctx, resources.LocalEnvironment())
	require.NoError(t, err)

	// config
	// global
	// - frontend
	// - global
	// - other_global
	// services
	// - svc1

	require.Equal(t, 4, len(loader.Configurations()))
	require.Equal(t, 2, len(loader.DNS()))

	dns := loader.DNS()[0]
	require.Equal(t, "localhost", dns.Host)
	require.Equal(t, uint32(8080), dns.Port)
	require.Equal(t, "rest", dns.Endpoint)
}

func TestFromService(t *testing.T) {
	service := &resources.Service{
		Name: "ServiceWithModule",
	}
	service.WithModule("mod")
	tcs := []struct {
		in      string
		service string
		module  string
		name    string
	}{
		{in: "auth0", name: "auth0"},
		{in: "other_app/store:postgres", name: "postgres", service: "store", module: "other_app"},
		{in: "store:postgres", name: "postgres", service: "store", module: "mod"},
	}

	for _, tc := range tcs {
		t.Run(tc.in, func(t *testing.T) {
			identity, err := service.Identity()
			res, err := configurations.FromService(identity, tc.in)
			require.NoError(t, err)
			require.Equal(t, res.Name, tc.name)
			if tc.service != "" {
				require.Equal(t, res.ServiceWithModule.Name, tc.service)
			}
			if tc.module != "" {
				require.Equal(t, res.ServiceWithModule.Module, tc.module)
			}
		})
	}
}

func TestExtract(t *testing.T) {
	p := "modules/app/services/ServiceWithModule"
	out := configurations.ExtractFromPath(p)
	require.Equal(t, "app/ServiceWithModule", out)
}

type testConfig struct {
	Top    string
	Nested struct {
		Value string
	}
}

func writeConfigurationFile(t *testing.T, root, name, content string) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(name))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
}

func TestReferenceOnlyManifestFormats(t *testing.T) {
	dir := t.TempDir()
	writeConfigurationFile(t, dir, "auth.env", "CLIENT_ID=public-client\n")
	writeConfigurationFile(t, dir, "auth.secret.ref.env", "# provider references are commit-safe\r\nCLIENT_SECRET=op://dev-vault/auth/client-secret\r\n")
	writeConfigurationFile(t, dir, "nested/database.secret.ref.yaml", `credentials:
  password: op://dev-vault/database/password
  replicas:
    - op://dev-vault/database/replica-password
`)

	infos, err := configurations.LoadConfigurationInformationsFromFiles(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, infos, 2)
	require.Equal(t, []string{"auth", "nested/database"}, []string{infos[0].Name, infos[1].Name})

	auth := infos[0]
	require.Len(t, auth.ConfigurationValues, 2)
	require.Equal(t, "CLIENT_ID", auth.ConfigurationValues[0].Key)
	require.False(t, auth.ConfigurationValues[0].Secret)
	require.Equal(t, "CLIENT_SECRET", auth.ConfigurationValues[1].Key)
	require.True(t, auth.ConfigurationValues[1].Secret)

	database := infos[1]
	require.NotNil(t, database.Data)
	require.Equal(t, "yaml", database.Data.Kind)
	require.True(t, database.Data.Secret)
}

func TestReferenceOnlyManifestRejectsUnsafeScalars(t *testing.T) {
	const canary = "PLAINTEXT_SECRET_CANARY"
	tests := []struct {
		name     string
		filename string
		content  string
		want     string
	}{
		{name: "env plaintext", filename: "auth.secret.ref.env", content: "TOKEN=" + canary + "\n", want: "plaintext is not allowed"},
		{name: "env unknown scheme", filename: "auth.secret.ref.env", content: "TOKEN=vault://" + canary + "/field\n", want: `unknown secret provider scheme "vault"`},
		{name: "env empty reference", filename: "auth.secret.ref.env", content: "TOKEN=op://\n", want: `invalid "op" secret provider reference`},
		{name: "yaml root", filename: "auth.secret.ref.yaml", content: canary + "\n", want: "secret at $"},
		{name: "yaml map", filename: "auth.secret.ref.yaml", content: "token: " + canary + "\n", want: `secret at $["token"]`},
		{name: "yaml nested map", filename: "auth.secret.ref.yaml", content: "nested:\n  token: " + canary + "\n", want: `secret at $["nested"]["token"]`},
		{name: "yaml array", filename: "auth.secret.ref.yaml", content: "tokens:\n  - op://dev-vault/auth/token\n  - " + canary + "\n", want: `secret at $["tokens"][1]`},
		{name: "yaml non-string", filename: "auth.secret.ref.yaml", content: "token: 42\n", want: "is plaintext"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeConfigurationFile(t, dir, tc.filename, tc.content)
			_, err := configurations.LoadConfigurationInformationsFromFiles(context.Background(), dir)
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.want)
			require.NotContains(t, err.Error(), canary)
		})
	}
}

func TestReferenceOnlyManifestRejectsDuplicateSecretDefinitionsDeterministically(t *testing.T) {
	dir := t.TempDir()
	writeConfigurationFile(t, dir, "database.secret.env", "PASSWORD=local-only-value\n")
	writeConfigurationFile(t, dir, "database.secret.ref.env", "PASSWORD=op://dev-vault/database/password\n")

	_, firstErr := configurations.LoadConfigurationInformationsFromFiles(context.Background(), dir)
	_, secondErr := configurations.LoadConfigurationInformationsFromFiles(context.Background(), dir)
	require.Error(t, firstErr)
	require.ErrorIs(t, firstErr, configurations.ErrConfigurationConflict)
	require.EqualError(t, secondErr, firstErr.Error())
	require.Contains(t, firstErr.Error(), `configuration "database"`)
	require.Contains(t, firstErr.Error(), "database.secret.env")
	require.Contains(t, firstErr.Error(), "database.secret.ref.env")
}

func TestConfigurationDataDefinitionErrorsIncludeBothSources(t *testing.T) {
	dir := t.TempDir()
	writeConfigurationFile(t, dir, "database.yaml", "host: localhost\n")
	writeConfigurationFile(t, dir, "database.secret.ref.env", "PASSWORD=op://dev-vault/database/password\n")

	_, err := configurations.LoadConfigurationInformationsFromFiles(context.Background(), dir)
	require.Error(t, err)
	require.ErrorIs(t, err, configurations.ErrConfigurationConflict)
	require.Contains(t, err.Error(), `configuration "database"`)
	require.Contains(t, err.Error(), "database.yaml")
	require.Contains(t, err.Error(), "database.secret.ref.env")
}

func TestLegacySecretFilesRemainLocalPlaintextEscapeHatch(t *testing.T) {
	dir := t.TempDir()
	writeConfigurationFile(t, dir, "legacy.secret.env", "TOKEN=local-only-value\n")

	infos, err := configurations.LoadConfigurationInformationsFromFiles(context.Background(), dir)
	require.NoError(t, err)
	require.Len(t, infos, 1)
	require.Len(t, infos[0].ConfigurationValues, 1)
	require.Equal(t, "local-only-value", infos[0].ConfigurationValues[0].Value)
	require.True(t, infos[0].ConfigurationValues[0].Secret)
}

func TestConfigurationFileOrderIsDeterministic(t *testing.T) {
	dir := t.TempDir()
	writeConfigurationFile(t, dir, "z-last.env", "KEY=value\n")
	writeConfigurationFile(t, dir, "a-first.env", "KEY=value\n")
	writeConfigurationFile(t, dir, "nested/middle.secret.ref.env", "KEY=op://dev-vault/item/field\n")
	want := []string{"a-first", "nested/middle", "z-last"}

	for range 20 {
		infos, err := configurations.LoadConfigurationInformationsFromFiles(context.Background(), dir)
		require.NoError(t, err)
		got := make([]string, 0, len(infos))
		for _, info := range infos {
			got = append(got, info.Name)
		}
		require.Equal(t, want, got)
	}
}
