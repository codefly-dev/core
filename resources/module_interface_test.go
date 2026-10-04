package resources

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/stretchr/testify/require"
)

func writeInterfaceFixture(t *testing.T, moduleYAML string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, ModuleConfigurationName), []byte(moduleYAML), 0o644))
	serviceDir := filepath.Join(dir, "services", "accounts")
	require.NoError(t, os.MkdirAll(serviceDir, 0o755))
	service := `kind: service
name: accounts
version: 0.0.0
agent:
    kind: runtime::service
    name: go-grpc
    version: 0.0.16
    publisher: codefly.ai
endpoints:
    - name: grpc
      api: grpc
`
	require.NoError(t, os.WriteFile(filepath.Join(serviceDir, "service.codefly.yaml"), []byte(service), 0o644))
	return dir
}

func TestLoadModuleFromDirRejectsInvalidInterface(t *testing.T) {
	dir := writeInterfaceFixture(t, `kind: module
name: billing
services:
    - name: accounts
interface:
    endpoints:
        - service: ghost
          endpoint: grpc
          visibility: public
`)
	_, err := LoadModuleFromDir(context.Background(), dir)
	require.Error(t, err)
	require.ErrorContains(t, err, "invalid interface")
}

// An interface entry is the whole export declaration, so it is judged on its
// own: an internal entry names the modules it exports to or is refused (it
// would export to nobody), a public one lists none (nothing reads it), and an
// undecorated entry is internal — it never silently grants every module.
func TestInterfaceEntryIsJudgedOnItsOwn(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct{ entry, says string }{
		"internal to nobody":        {"          visibility: internal\n", "to no module"},
		"undecorated":               {"", "to no module"},
		"public with an allow-list": {"          visibility: public\n          allow-modules: [platform]\n", `allow-modules with visibility "public"`},
		// The schema refuses these before the interface validator sees them.
		"the former module spelling": {"          visibility: module\n", "visibility"},
		"a private export":           {"          visibility: private\n", "visibility"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeInterfaceFixture(t, "kind: module\nname: billing\nservices:\n    - name: accounts\ninterface:\n    endpoints:\n        - service: accounts\n          endpoint: grpc\n"+tc.entry)
			_, err := LoadModuleFromDir(ctx, dir)
			require.ErrorContains(t, err, tc.says)
		})
	}
	for _, spelling := range []string{"module", "private", "external"} {
		ie := &InterfaceEndpoint{Service: "accounts", Endpoint: "grpc", Visibility: spelling}
		require.ErrorContains(t, ie.validate(), "invalid visibility", "the validator refuses the spelling on its own, whatever the schema does first")
	}
	dir := writeInterfaceFixture(t, "kind: module\nname: billing\nservices:\n    - name: accounts\ninterface:\n    endpoints:\n        - service: accounts\n          endpoint: grpc\n          allow-modules: [platform]\n")
	mod, err := LoadModuleFromDir(ctx, dir)
	require.NoError(t, err)
	service, err := mod.LoadServiceFromName(ctx, "accounts")
	require.NoError(t, err)
	require.Equal(t, VisibilityInternal, service.Endpoints[0].Visibility, "an undecorated entry exports at internal")
	require.Equal(t, []string{"platform"}, service.Endpoints[0].AllowModules)
	proto, err := mod.Proto(ctx)
	require.NoError(t, err)
	require.Equal(t, VisibilityInternal, proto.GetInterface().GetEndpoints()[0].GetVisibility())
	require.Equal(t, []string{"platform"}, proto.GetInterface().GetEndpoints()[0].GetAllowModules())
	require.NoError(t, service.Save(ctx))
	saved, err := os.ReadFile(filepath.Join(dir, "services", "accounts", "service.codefly.yaml"))
	require.NoError(t, err)
	require.NotContains(t, string(saved), "allow-modules", "the entry's list is the module's, never written into the service")
}

// The wire carries the same rule as the loader: a raw InterfaceEndpoint proto
// with an allow-list its visibility never reads, or an internal export to
// nobody, fails schema validation; and a Module built in memory cannot publish
// such an entry through Proto().
func TestInterfaceEntryRulesHoldOnTheWire(t *testing.T) {
	ctx := context.Background()
	for name, entry := range map[string]*basev0.InterfaceEndpoint{
		"public with an allow-list": {Service: "api", Endpoint: "grpc", Visibility: VisibilityPublic, AllowModules: []string{"payments"}},
		"internal to nobody":        {Service: "api", Endpoint: "grpc", Visibility: VisibilityInternal},
		"a private export":          {Service: "api", Endpoint: "grpc", Visibility: VisibilityPrivate, AllowModules: []string{"payments"}},
	} {
		t.Run(name, func(t *testing.T) {
			require.Error(t, Validate(&basev0.Module{Name: "billing", Interface: &basev0.ModuleInterface{Endpoints: []*basev0.InterfaceEndpoint{entry}}}), "schema")
			mod := &Module{Name: "billing", Interface: &ModuleInterface{Endpoints: []*InterfaceEndpoint{{Service: entry.Service, Endpoint: entry.Endpoint, Visibility: entry.Visibility, AllowModules: entry.AllowModules}}}}
			_, err := mod.Proto(ctx)
			require.Error(t, err, "conversion")
		})
	}
	for _, entry := range []*basev0.InterfaceEndpoint{
		{Service: "api", Endpoint: "grpc", Visibility: VisibilityPublic},
		{Service: "api", Endpoint: "grpc", Visibility: VisibilityInternal, AllowModules: []string{"payments"}},
	} {
		require.NoError(t, Validate(&basev0.Module{Name: "billing", Interface: &basev0.ModuleInterface{Endpoints: []*basev0.InterfaceEndpoint{entry}}}))
	}
}

func TestLoadModuleFromDirAcceptsValidInterface(t *testing.T) {
	dir := writeInterfaceFixture(t, `kind: module
name: billing
services:
    - name: accounts
interface:
    endpoints:
        - service: accounts
          endpoint: grpc
          visibility: public
`)
	mod, err := LoadModuleFromDir(context.Background(), dir)
	require.NoError(t, err)
	require.True(t, mod.HasInterface())
}

func TestExportedEndpointsForPackageRequiresInterface(t *testing.T) {
	ctx := context.Background()

	withInterface := writeInterfaceFixture(t, `kind: module
name: billing
services:
    - name: accounts
interface:
    endpoints:
        - service: accounts
          endpoint: grpc
          visibility: public
`)
	mod, err := LoadModuleFromDir(ctx, withInterface)
	require.NoError(t, err)
	exported, err := mod.ExportedEndpointsForPackage(ctx)
	require.NoError(t, err)
	require.Len(t, exported, 1)

	withoutInterface := writeInterfaceFixture(t, `kind: module
name: billing
services:
    - name: accounts
`)
	mod, err = LoadModuleFromDir(ctx, withoutInterface)
	require.NoError(t, err)
	_, err = mod.ExportedEndpointsForPackage(ctx)
	require.Error(t, err)
	require.ErrorContains(t, err, "an interface is required")
}
