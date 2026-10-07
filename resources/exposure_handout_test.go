package resources_test

import (
	"context"
	"strings"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func handoutMapping(name, visibility, exposure string, access *basev0.NetworkAccess) *basev0.NetworkMapping {
	instance := resources.NewNetworkInstance("localhost", 9090)
	instance.Access = access
	return &basev0.NetworkMapping{
		Endpoint:  &basev0.Endpoint{Module: "saas", Service: "accounts", Name: name, Api: "grpc", Visibility: visibility, Exposure: exposure},
		Instances: []*basev0.NetworkInstance{instance},
	}
}

// The public split is an addressing split keyed on exposure, and it judges the
// declaration whole before it hands anything out: a public endpoint that
// states no exposure, or an exposure its reach contradicts, is refused there
// rather than sorted into either side.
func TestSplitPublicNetworkMappingsJudgesTheDeclaration(t *testing.T) {
	ctx := context.Background()
	exposed := handoutMapping("web", resources.VisibilityPublic, resources.ExposurePublic, resources.NewPublicNetworkAccess())
	unexposed := handoutMapping("grpc", resources.VisibilityPublic, resources.ExposureNone, resources.NewNativeNetworkAccess())
	public, nonPublic, err := resources.SplitPublicNetworkMappings(ctx, []*basev0.NetworkMapping{exposed, unexposed})
	require.NoError(t, err)
	require.Len(t, public, 1)
	require.Equal(t, "web", public[0].GetEndpoint().GetName(), "the split follows exposure, not visibility")
	require.Len(t, nonPublic, 1)

	for name, broken := range map[string]*basev0.NetworkMapping{
		"a public endpoint stating no exposure":  handoutMapping("grpc", resources.VisibilityPublic, "", resources.NewNativeNetworkAccess()),
		"an exposure the reach contradicts":      handoutMapping("grpc", resources.VisibilityInternal, resources.ExposurePublic, resources.NewPublicNetworkAccess()),
		"a visibility the model does not define": handoutMapping("grpc", "module", "", resources.NewNativeNetworkAccess()),
	} {
		t.Run(name, func(t *testing.T) {
			_, _, err := resources.SplitPublicNetworkMappings(ctx, []*basev0.NetworkMapping{exposed, broken})
			require.ErrorIs(t, err, resources.ErrInvalidEndpointDeclaration)
		})
	}
}

// The environment-variable hand-out judges each declaration whole, and its
// public prefix follows exposure: an exposed endpoint's carrier takes the
// public prefix, a public endpoint with none takes the other, whatever the
// visibility says.
func TestEnvironmentVariableHandoutJudgesAndPrefixesByExposure(t *testing.T) {
	ctx := context.Background()
	native := resources.NewNativeNetworkAccess()
	exposed := handoutMapping("web", resources.VisibilityPublic, resources.ExposurePublic, native)
	unexposed := handoutMapping("grpc", resources.VisibilityPublic, resources.ExposureNone, native)
	internal := handoutMapping("usage", resources.VisibilityInternal, "", native)

	manager := resources.NewEnvironmentVariableManager()
	require.NoError(t, manager.AddEndpoints(ctx, []*basev0.NetworkMapping{exposed, unexposed, internal}, native,
		resources.WithPublicEnvironmentVariablePrefix("OUTWARD_"), resources.WithNonPublicEnvironmentVariablePrefix("INWARD_")))
	variables, err := manager.All()
	require.NoError(t, err)
	prefixOf := map[string]string{}
	for _, variable := range variables {
		for _, name := range []string{"WEB", "GRPC", "USAGE"} {
			if strings.Contains(variable.Key, "__ACCOUNTS__"+name+"__") {
				prefixOf[name] = strings.SplitN(variable.Key, "CODEFLY__", 2)[0]
			}
		}
	}
	require.Equal(t, "OUTWARD_", prefixOf["WEB"], "an exposed endpoint takes the public prefix")
	require.Equal(t, "INWARD_", prefixOf["GRPC"], "a public endpoint with no outward address does not — visibility is not addressing")
	require.Equal(t, "INWARD_", prefixOf["USAGE"])

	for name, broken := range map[string]*basev0.NetworkMapping{
		"a public endpoint stating no exposure": handoutMapping("grpc", resources.VisibilityPublic, "", native),
		"an exposure the reach contradicts":     handoutMapping("grpc", resources.VisibilityPrivate, resources.ExposurePublic, native),
	} {
		t.Run(name, func(t *testing.T) {
			fresh := resources.NewEnvironmentVariableManager()
			require.ErrorIs(t, fresh.AddEndpoints(ctx, []*basev0.NetworkMapping{broken}, native), resources.ErrInvalidEndpointDeclaration)
		})
	}
}
