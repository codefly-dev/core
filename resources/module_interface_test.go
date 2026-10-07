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
// own: it names the reach the module grants — internal (the default) or public
// — and it names nobody. An entry that authors an allow-list is refused by
// name, the wildcard and the empty list included: the list is derived from the
// consumers' declared dependencies, and a module writing it would be naming
// its own consumers.
func TestInterfaceEntryIsJudgedOnItsOwn(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct{ entry, says string }{
		// The forbidden key is refused by PRESENCE, before decoding: any value
		// and any spelling.
		"an internal allow-list": {"          visibility: internal\n          allow-modules: [platform]\n", `authors allow-modules (key "allow-modules")`},
		"a wildcard":             {"          visibility: internal\n          allow-modules: [\"*\"]\n", `authors allow-modules (key "allow-modules")`},
		"an empty list":          {"          visibility: internal\n          allow-modules: []\n", `authors allow-modules (key "allow-modules")`},
		// A null is refused by the manifest's shared node check before the
		// entry's own judgement is reached: no field of a manifest takes one.
		"null":                       {"          visibility: internal\n          allow-modules: null\n", "explicit null at interface.endpoints[0].allow-modules"},
		"the underscore spelling":    {"          visibility: internal\n          allow_modules: [platform]\n", `authors allow-modules (key "allow_modules")`},
		"the camel spelling":         {"          visibility: internal\n          allowModules: [platform]\n", `authors allow-modules (key "allowModules")`},
		"public with an allow-list":  {"          visibility: public\n          allow-modules: [platform]\n", `authors allow-modules (key "allow-modules")`},
		"an undecorated allow-list":  {"          allow-modules: [platform]\n", `authors allow-modules (key "allow-modules")`},
		"an unknown key":             {"          visibilty: public\n", `declares unknown key "visibilty"`},
		"the former module spelling": {"          visibility: module\n", "visibility"},
		"a private export":           {"          visibility: private\n", "visibility"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := writeInterfaceFixture(t, "kind: module\nname: billing\nservices:\n    - name: accounts\ninterface:\n    endpoints:\n        - service: accounts\n          endpoint: grpc\n"+tc.entry)
			_, err := LoadModuleFromDir(ctx, dir)
			require.ErrorContains(t, err, tc.says)
			require.ErrorIs(t, err, ErrInvalidEndpointDeclaration, "an interface entry the model refuses is an invalid declaration, by the one sentinel")
		})
	}
	for _, spelling := range []string{"module", "private", "external"} {
		ie := &InterfaceEndpoint{Service: "accounts", Endpoint: "grpc", Visibility: spelling}
		err := ie.validate()
		require.ErrorContains(t, err, "invalid visibility", "the validator refuses the spelling on its own, whatever the schema does first")
		require.ErrorIs(t, err, ErrInvalidEndpointDeclaration)
	}
	// An export to the composition names nobody: an undecorated entry and an
	// explicit internal one both load, export at internal, and publish so.
	for name, entry := range map[string]string{"undecorated": "", "internal": "          visibility: internal\n"} {
		t.Run(name, func(t *testing.T) {
			dir := writeInterfaceFixture(t, "kind: module\nname: billing\nservices:\n    - name: accounts\ninterface:\n    endpoints:\n        - service: accounts\n          endpoint: grpc\n"+entry)
			mod, err := LoadModuleFromDir(ctx, dir)
			require.NoError(t, err)
			service, err := mod.LoadServiceFromName(ctx, "accounts")
			require.NoError(t, err)
			require.Equal(t, VisibilityInternal, service.Endpoints[0].Visibility, "the entry exports at internal")
			require.True(t, service.Endpoints[0].AllowsModule("anything"), "internal is reachable by whatever composes the workspace")
			proto, err := mod.Proto(ctx)
			require.NoError(t, err)
			require.Equal(t, VisibilityInternal, proto.GetInterface().GetEndpoints()[0].GetVisibility())
			require.NoError(t, service.Save(ctx))
			saved, err := os.ReadFile(filepath.Join(dir, "services", "accounts", "service.codefly.yaml"))
			require.NoError(t, err)
			require.NotContains(t, string(saved), "allow-modules", "nothing ever writes an allow-list into a service")
			require.NotContains(t, string(saved), "visibility: internal", "the exported visibility is the module's, never written into the service")
		})
	}
}

// The wire carries the same rule as the loader: an InterfaceEndpoint proto has
// no allow_modules field at all (the number and the name are reserved), a
// private export fails schema validation, an internal export to nobody is
// valid, and a Module built in memory cannot publish an entry that authored a
// list through Proto().
func TestInterfaceEntryRulesHoldOnTheWire(t *testing.T) {
	ctx := context.Background()
	require.Error(t, Validate(&basev0.Module{Name: "billing", Interface: &basev0.ModuleInterface{Endpoints: []*basev0.InterfaceEndpoint{
		{Service: "api", Endpoint: "grpc", Visibility: VisibilityPrivate},
	}}}), "a private export is refused by the schema")
	for _, entry := range []*basev0.InterfaceEndpoint{
		{Service: "api", Endpoint: "grpc", Visibility: VisibilityPublic},
		{Service: "api", Endpoint: "grpc", Visibility: VisibilityInternal},
	} {
		require.NoError(t, Validate(&basev0.Module{Name: "billing", Interface: &basev0.ModuleInterface{Endpoints: []*basev0.InterfaceEndpoint{entry}}}))
	}
	descriptor := (&basev0.InterfaceEndpoint{}).ProtoReflect().Descriptor()
	require.Nil(t, descriptor.Fields().ByName("allow_modules"), "the wire model carries no allow-list")
	require.True(t, descriptor.ReservedNames().Has("allow_modules"), "the name is reserved")
	require.True(t, descriptor.ReservedRanges().Has(4), "the number is reserved")
	for name, authored := range map[string][]string{"a module": {"payments"}, "the wildcard": {"*"}, "an empty list": {}} {
		t.Run(name, func(t *testing.T) {
			mod := &Module{Name: "billing", Interface: &ModuleInterface{Endpoints: []*InterfaceEndpoint{{Service: "api", Endpoint: "grpc", Visibility: VisibilityInternal, AllowModules: authored}}}}
			_, err := mod.Proto(ctx)
			require.ErrorContains(t, err, "authors allow-modules")
		})
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
