package dependencies

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	v0 "github.com/codefly-dev/core/generated/go/codefly/cli/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
)

// servingCLI is a CLI that serves whatever dependency mappings it is given —
// a provider that judged nothing — so the session's own judgement is what the
// test observes.
type servingCLI struct {
	v0.CLIClient
	mappings []*basev0.NetworkMapping
}

func (cli *servingCLI) GetDependenciesNetworkMappings(context.Context, *v0.GetNetworkMappingsRequest, ...grpc.CallOption) (*v0.GetNetworkMappingsResponse, error) {
	return &v0.GetNetworkMappingsResponse{NetworkMappings: cli.mappings}, nil
}

func (cli *servingCLI) GetConfiguration(context.Context, *v0.GetConfigurationRequest, ...grpc.CallOption) (*v0.GetConfigurationResponse, error) {
	return &v0.GetConfigurationResponse{}, nil
}

func (cli *servingCLI) GetDependenciesConfigurations(context.Context, *v0.GetConfigurationRequest, ...grpc.CallOption) (*v0.GetConfigurationsResponse, error) {
	return &v0.GetConfigurationsResponse{}, nil
}

func writeFile(t *testing.T, root, relative, content string) {
	t.Helper()
	file := filepath.Join(root, relative)
	require.NoError(t, os.MkdirAll(filepath.Dir(file), 0o755))
	require.NoError(t, os.WriteFile(file, []byte(content), 0o600))
}

const consumerManifest = "kind: service\nname: api\nversion: 0.0.1\nagent:\n  kind: runtime::service\n  name: go-grpc\n  version: 0.0.1\n  publisher: codefly.ai\nservice-dependencies:\n  - name: accounts\n    module: saas\n    kind: runtime\n    endpoints:\n      - name: usage\n"

// moduleConsumer is a service of module platform, in a workspace that also
// carries saas, depending on saas/accounts/usage at run time.
func moduleConsumer(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "workspace.codefly.yaml", "name: plain\nlayout: modules\nmodules:\n  - name: platform\n  - name: saas\n")
	writeFile(t, root, "modules/platform/module.codefly.yaml", "kind: module\nname: platform\nservices:\n  - name: api\n")
	writeFile(t, root, "modules/platform/services/api/service.codefly.yaml", consumerManifest)
	writeFile(t, root, "modules/saas/module.codefly.yaml", "kind: module\nname: saas\nservices: []\n")
	return filepath.Join(root, "modules", "platform", "services", "api")
}

// solutionConsumer is the same service, as a service of a SOLUTION the product
// declares beside the platform workspace it composes.
func solutionConsumer(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "core/workspace.codefly.yaml", "name: platform-core\nlayout: modules\nmodules:\n  - name: saas\n")
	writeFile(t, root, "core/modules/saas/module.codefly.yaml", "kind: module\nname: saas\nservices: []\n")
	writeFile(t, root, "product/workspace.codefly.yaml", "name: product\nlayout: modules\nworkspaces:\n  - name: platform-core\n    path: ../core\nsolutions:\n  - name: wiki\n    path: solutions/wiki\n")
	writeFile(t, root, "product/solutions/wiki/module.codefly.yaml", "kind: module\nname: wiki\nservices:\n  - name: api\n")
	writeFile(t, root, "product/solutions/wiki/services/api/service.codefly.yaml", consumerManifest)
	return filepath.Join(root, "product", "solutions", "wiki", "services", "api")
}

func usageMapping(visibility string) *basev0.NetworkMapping {
	instance := resources.NewNetworkInstance("localhost", 19090)
	instance.Access = resources.NewNativeNetworkAccess()
	return &basev0.NetworkMapping{
		Endpoint:  &basev0.Endpoint{Module: "saas", Service: "accounts", Name: "usage", Api: "grpc", Visibility: visibility},
		Instances: []*basev0.NetworkInstance{instance},
	}
}

// The session judges the addresses the CLI serves it with the composition it
// finds above its directory, so a provider that judged nothing is not
// trusted: a private endpoint of another module is refused, a solution's
// route to a module is refused, and a permitted one is projected.
func TestTheSessionJudgesWhatTheCLIServesWithTheWorkspaceItFinds(t *testing.T) {
	ctx := context.Background()
	session := func(dir string, mappings ...*basev0.NetworkMapping) *Dependencies {
		return &Dependencies{cli: &servingCLI{mappings: mappings}, dir: dir, runtimeContext: resources.NewRuntimeContextNative()}
	}

	_, err := session(moduleConsumer(t), usageMapping(resources.VisibilityPrivate)).resolveEnvironment(ctx)
	require.ErrorContains(t, err, `private to module "saas"`, "a private endpoint the CLI served anyway is refused by the session")

	env, err := session(moduleConsumer(t), usageMapping(resources.VisibilityInternal)).resolveEnvironment(ctx)
	require.NoError(t, err)
	require.Equal(t, "localhost:19090", env.values["CODEFLY__ENDPOINT__SAAS__ACCOUNTS__USAGE__GRPC"], "a permitted endpoint is projected")

	_, err = session(solutionConsumer(t), usageMapping(resources.VisibilityInternal)).resolveEnvironment(ctx)
	require.ErrorIs(t, err, resources.ErrSolutionReachesThroughHost, "a solution reaches modules only through the host, whatever the CLI served")
}
