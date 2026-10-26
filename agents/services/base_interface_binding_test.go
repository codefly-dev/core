package services

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	"github.com/codefly-dev/core/resources"
)

// An agent serving a service in a workspace that imports another by version
// cannot load that workspace: only the host resolves the import. With the
// host's binding in the identity it loads, binding as the host did; without
// it, it refuses as before.
func TestLoadBindsInterfacesFromTheHost(t *testing.T) {
	root := t.TempDir()
	agent := "agent:\n    kind: codefly:service\n    name: go-grpc\n    version: 0.0.1\n    publisher: codefly.dev\n"
	for name, content := range map[string]string{
		"workspace.codefly.yaml":              "name: product\nlayout: modules\nworkspaces:\n    - name: platform-core\n      source: team/platform-core\n      version: 1.0.0\nmodules:\n    - name: records\n",
		"modules/records/module.codefly.yaml": "kind: module\nname: records\nservices:\n    - name: records\n",
		"modules/records/services/records/service.codefly.yaml": "kind: service\nname: records\nversion: 0.0.1\n" + agent +
			"endpoints:\n    - name: grpc\n      api: grpc\nservice-dependencies:\n    - interface: codefly.dev/authority@0.1.0\n      kind: external\n",
	} {
		path := filepath.Join(root, name)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	identity := func(bindings *basev0.ServiceInterfaceBindings) *basev0.ServiceIdentity {
		return &basev0.ServiceIdentity{
			Name: "records", Module: "records", Workspace: "product", Version: "0.0.1",
			WorkspacePath: root, RelativeToWorkspace: "modules/records/services/records",
			InterfaceBindings: bindings,
		}
	}
	ctx := context.Background()
	serviceAgent := &resources.Agent{Kind: resources.ServiceAgent, Name: "go-grpc", Version: "0.0.1", Publisher: "codefly.dev"}

	base := NewServiceBase(ctx, serviceAgent)
	require.ErrorContains(t, base.Load(ctx, identity(nil), &struct{}{}), "requires host resolution")

	base = NewServiceBase(ctx, serviceAgent)
	require.NoError(t, base.Load(ctx, identity(&basev0.ServiceInterfaceBindings{}), &struct{}{}))
	require.True(t, base.Service.ServiceDependencies[0].Unbound(), "nothing in the host's composition implements it")

	base = NewServiceBase(ctx, serviceAgent)
	require.NoError(t, base.Load(ctx, identity(&basev0.ServiceInterfaceBindings{Bindings: []*basev0.ServiceInterfaceBinding{
		{Interface: "codefly.dev/authority@0.1.0", Module: "authority", Service: "gateway", Endpoint: "grpc"},
	}}), &struct{}{}))
	dependency := base.Service.ServiceDependencies[0]
	require.Equal(t, "authority/gateway", dependency.Unique())
	require.Len(t, dependency.Endpoints, 1)
	require.Equal(t, "grpc", dependency.Endpoints[0].Name)
}
