package resources_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/standards"
)

const (
	cacheDefinitionDir   = "testdata/interfaces/cache-0.3.0"
	widgetsDefinitionDir = "testdata/interfaces/widgets-1.2.0"
)

func loadDefinition(t *testing.T, dir string) *resources.Interface {
	t.Helper()
	definition, err := resources.LoadInterfaceFromDir(context.Background(), dir)
	require.NoError(t, err)
	return definition
}

func TestInterfaceIdentityIsExactAndStrict(t *testing.T) {
	identity, err := resources.ParseInterfaceIdentity("codefly.dev/cache@0.3.0")
	require.NoError(t, err)
	require.Equal(t, "codefly.dev", identity.Publisher)
	require.Equal(t, "cache", identity.Name)
	require.Equal(t, "codefly.dev/cache", identity.Key())
	require.Equal(t, "codefly.dev/cache@0.3.0", identity.String())

	for _, invalid := range []string{
		"codefly.dev/cache",        // no version
		"codefly.dev/cache@v0.3.0", // not a strict version
		"codefly.dev/cache@^0.3",   // a range is a requirement, not an identity
		"cache@0.3.0",              // no publisher
		"Codefly.dev/cache@0.3.0",  // publisher is lowercase
		"codefly.dev/a/b@0.3.0",    // name is one segment
	} {
		_, err := resources.ParseInterfaceIdentity(invalid)
		require.Error(t, err, invalid)
	}
}

func TestInterfaceRequirementAdmitsItsRangeOnly(t *testing.T) {
	requirement, err := resources.ParseInterfaceRequirement("codefly.dev/cache@^0.3")
	require.NoError(t, err)
	satisfies := func(value string) bool {
		identity, err := resources.ParseInterfaceIdentity(value)
		require.NoError(t, err)
		return requirement.Satisfies(identity)
	}
	require.True(t, satisfies("codefly.dev/cache@0.3.0"))
	require.True(t, satisfies("codefly.dev/cache@0.3.7"))
	require.False(t, satisfies("codefly.dev/cache@0.4.0"), "below 1.0.0 a minor is breaking")
	require.False(t, satisfies("example.dev/cache@0.3.0"), "same name, other publisher")

	_, err = resources.ParseInterfaceRequirement("codefly.dev/cache")
	require.Error(t, err, "a requirement must state its range")
}

func TestInterfaceEndpointTypesAreEndpointAPIs(t *testing.T) {
	for _, interfaceType := range resources.InterfaceTypes() {
		if !interfaceType.ImplementedByEndpoint() {
			continue
		}
		require.True(t, slices.Contains(standards.APIS(), string(interfaceType)), "%s must be an endpoint API", interfaceType)
	}
	require.False(t, slices.Contains(resources.InterfaceTypes(), resources.InterfaceType(standards.TCP)), "tcp is a transport and carries no interface")
}

func TestInterfaceDefinitionLoadsStrictly(t *testing.T) {
	cache := loadDefinition(t, cacheDefinitionDir)
	require.Equal(t, "codefly.dev/cache@0.3.0", cache.Identity().String())
	require.Equal(t, resources.InterfaceTypeCapability, cache.Type)

	dir := t.TempDir()
	content, err := os.ReadFile(filepath.Join(cacheDefinitionDir, resources.InterfaceConfigurationName))
	require.NoError(t, err)
	misspelled := []byte(string(content) + "          optionnal: true\n")
	require.NoError(t, os.WriteFile(filepath.Join(dir, resources.InterfaceConfigurationName), misspelled, 0o600))
	_, err = resources.LoadInterfaceFromDir(context.Background(), dir)
	require.Error(t, err, "a misspelled modifier must not load as its opposite")
}

func TestInterfaceSurfaceMustMatchItsType(t *testing.T) {
	widgets := loadDefinition(t, widgetsDefinitionDir)
	widgets.Capability = &resources.InterfaceCapability{Configuration: "connection", Keys: []*resources.InterfaceCapabilityKey{{Name: "url"}}}
	require.ErrorContains(t, widgets.Validate(), "declares a capability section")

	widgets = loadDefinition(t, widgetsDefinitionDir)
	widgets.Protobuf = nil
	require.ErrorContains(t, widgets.Validate(), "must declare its protobuf surface")

	widgets = loadDefinition(t, widgetsDefinitionDir)
	widgets.Protobuf.Services[0].Methods = append(widgets.Protobuf.Services[0].Methods, "GetWidget")
	require.ErrorContains(t, widgets.Validate(), "declared twice")
}

func TestInterfaceSaveRoundTrips(t *testing.T) {
	ctx := context.Background()
	widgets := loadDefinition(t, widgetsDefinitionDir)
	dir := t.TempDir()
	require.NoError(t, widgets.SaveToDir(ctx, dir))
	reloaded, err := resources.LoadInterfaceFromDir(ctx, dir)
	require.NoError(t, err)
	require.Equal(t, widgets, reloaded)
}

func connection(values ...*basev0.ConfigurationValue) *basev0.Configuration {
	return &basev0.Configuration{
		Origin: "platform/redis",
		Infos:  []*basev0.ConfigurationInformation{{Name: "connection", ConfigurationValues: values}},
	}
}

func TestCapabilityConfigurationMustConform(t *testing.T) {
	cache := loadDefinition(t, cacheDefinitionDir)
	url := &basev0.ConfigurationValue{Key: "url", Value: "redis://cache:6379"}
	password := &basev0.ConfigurationValue{Key: "password", Value: "s3cret", Secret: true}

	require.NoError(t, cache.ValidateConfiguration(connection(url, password)), "the optional key may be absent")

	require.ErrorContains(t, cache.ValidateConfiguration(connection(url)), `required key "password" is missing`)
	require.ErrorContains(t, cache.ValidateConfiguration(connection(url, &basev0.ConfigurationValue{Key: "password", Value: "s3cret"})),
		`key "password" must have secret=true`)
	require.ErrorContains(t, cache.ValidateConfiguration(connection(url, password, &basev0.ConfigurationValue{Key: "host", Value: "cache"})),
		`key "host" is not part of the interface`)
	require.ErrorContains(t, cache.ValidateConfiguration(&basev0.Configuration{Origin: "platform/redis"}), `has no "connection" group`)

	widgets := loadDefinition(t, widgetsDefinitionDir)
	require.ErrorContains(t, widgets.ValidateConfiguration(connection(url)), "only a capability interface")
}

func evolve(t *testing.T, dir, version string, change func(*resources.Interface)) (*resources.InterfaceEvolution, error) {
	t.Helper()
	before := loadDefinition(t, dir)
	after := loadDefinition(t, dir)
	after.Version = version
	change(after)
	require.NoError(t, after.Validate())
	return resources.EvolveInterface(before, after)
}

func TestInterfaceEvolutionIsComputed(t *testing.T) {
	addMethod := func(i *resources.Interface) {
		i.Protobuf.Services[0].Methods = append(i.Protobuf.Services[0].Methods, "DeleteWidget")
	}
	removeMethod := func(i *resources.Interface) { i.Protobuf.Services[0].Methods = i.Protobuf.Services[0].Methods[:1] }

	evolution, err := evolve(t, widgetsDefinitionDir, "1.3.0", addMethod)
	require.NoError(t, err)
	require.True(t, evolution.Compatible())
	require.Equal(t, []string{"added procedure /widgets.v1.WidgetService/DeleteWidget"}, evolution.Additive)

	_, err = evolve(t, widgetsDefinitionDir, "1.2.1", addMethod)
	require.ErrorContains(t, err, "only bumps the patch", "an addition needs at least a minor")

	evolution, err = evolve(t, widgetsDefinitionDir, "1.3.0", removeMethod)
	require.ErrorContains(t, err, "the surface breaks", "a minor cannot carry a removal")
	require.Equal(t, []string{"removed procedure /widgets.v1.WidgetService/ListWidgets"}, evolution.Breaking)

	evolution, err = evolve(t, widgetsDefinitionDir, "2.0.0", removeMethod)
	require.NoError(t, err)
	require.False(t, evolution.Compatible())

	_, err = evolve(t, widgetsDefinitionDir, "1.2.0", func(*resources.Interface) {})
	require.ErrorContains(t, err, "must be greater")
}

func TestCapabilityEvolutionTreatsKeysAsTheContract(t *testing.T) {
	addOptional := func(i *resources.Interface) {
		i.Capability.Keys = append(i.Capability.Keys, &resources.InterfaceCapabilityKey{Name: "database", Optional: true})
	}
	addRequired := func(i *resources.Interface) {
		i.Capability.Keys = append(i.Capability.Keys, &resources.InterfaceCapabilityKey{Name: "database"})
	}
	requireTLS := func(i *resources.Interface) { i.Capability.Keys[2].Optional = false }
	renameGroup := func(i *resources.Interface) { i.Capability.Configuration = "redis" }

	// Below 1.0.0 the minor is the breaking component, so an addition and a
	// break both need one; a patch carries neither.
	_, err := evolve(t, cacheDefinitionDir, "0.3.1", addOptional)
	require.ErrorContains(t, err, "only bumps the patch")
	evolution, err := evolve(t, cacheDefinitionDir, "0.4.0", addOptional)
	require.NoError(t, err)
	require.True(t, evolution.Compatible())

	for name, change := range map[string]func(*resources.Interface){
		"a new required key":       addRequired,
		"an optional key required": requireTLS,
		"a renamed group":          renameGroup,
	} {
		evolution, err := evolve(t, cacheDefinitionDir, "0.3.1", change)
		require.ErrorContains(t, err, "the surface breaks", name)
		require.False(t, evolution.Compatible(), name)
	}
}

func TestResolveInterfacePinsTheIdentity(t *testing.T) {
	identity, err := resources.ParseInterfaceIdentity("codefly.dev/cache@0.3.0")
	require.NoError(t, err)

	_, err = resources.ResolveInterface(context.Background(), identity)
	require.ErrorContains(t, err, "no interface resolver")

	ctx := resources.WithInterfaceResolver(context.Background(), func(ctx context.Context, _ *resources.InterfaceIdentity) (*resources.Interface, error) {
		return resources.LoadInterfaceFromDir(ctx, widgetsDefinitionDir)
	})
	_, err = resources.ResolveInterface(ctx, identity)
	require.ErrorContains(t, err, "returned the definition of example.dev/widgets@1.2.0")

	ctx = resources.WithInterfaceResolver(context.Background(), definitionResolver)
	resolved, err := resources.ResolveInterface(ctx, identity)
	require.NoError(t, err)
	require.Equal(t, identity.String(), resolved.Identity().String())
}

// definitionResolver serves the published definitions under testdata/interfaces,
// the way a host serves the definitions it acquired and pinned.
func definitionResolver(ctx context.Context, identity *resources.InterfaceIdentity) (*resources.Interface, error) {
	dir, err := filepath.Abs(filepath.Join("testdata", "interfaces", identity.Name+"-"+identity.Version))
	if err != nil {
		return nil, err
	}
	return resources.LoadInterfaceFromDir(ctx, dir)
}
