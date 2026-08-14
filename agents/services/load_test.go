package services

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"testing/fstest"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/stretchr/testify/require"
)

func TestBuilderLoadServiceHandlesCreationMode(t *testing.T) {
	ctx := context.Background()
	identity := saveLoadTestService(t, ctx)
	base := NewServiceBase(ctx, &resources.Agent{Kind: resources.ServiceAgent, Name: "test", Version: "0.0.1"})
	factory := fstest.MapFS{
		"templates/factory/GETTING_STARTED.md.tmpl": {Data: []byte("# {{ .Service.Name.Title }}\n")},
	}

	response, err := base.Builder.LoadService(ctx, &builderv0.LoadRequest{
		Identity:     identity,
		CreationMode: &builderv0.CreationMode{},
	}, BuilderLoad{Settings: &struct{}{}, FactoryTemplates: factory})
	require.NoError(t, err)
	require.Equal(t, builderv0.LoadStatus_READY, response.GetState().GetState(), response.GetState().GetMessage())
	require.Equal(t, "# ExampleService\n", response.GetGettingStarted())
}

func TestBuilderInitRejectsAmbiguousSameAPIDependency(t *testing.T) {
	ctx := context.Background()
	identity := saveLoadTestService(t, ctx)
	base := NewServiceBase(ctx, &resources.Agent{Kind: resources.ServiceAgent, Name: "test", Version: "0.0.1"})
	response, err := base.Builder.LoadService(ctx, &builderv0.LoadRequest{Identity: identity}, BuilderLoad{Settings: &struct{}{}})
	require.NoError(t, err)
	require.Equal(t, builderv0.LoadStatus_READY, response.GetState().GetState())

	base.Service.ServiceDependencies = []*resources.ServiceDependency{{Module: "saas", Name: "accounts"}}
	base.DependencyEndpoints = []*basev0.Endpoint{
		{Module: "saas", Service: "accounts", Name: "grpc", Api: "grpc"},
		{Module: "saas", Service: "accounts", Name: "usage", Api: "grpc"},
	}
	initResponse, err := base.Builder.InitResponse()
	require.NoError(t, err)
	require.Equal(t, builderv0.InitStatus_ERROR, initResponse.GetState().GetState())
	require.Contains(t, initResponse.GetState().GetMessage(), "declare endpoint names")

	base.Service.ServiceDependencies[0].Endpoints = []*resources.EndpointReference{{Name: "usage"}}
	base.DependencyEndpoints = base.DependencyEndpoints[1:]
	initResponse, err = base.Builder.InitResponse()
	require.NoError(t, err)
	require.Equal(t, builderv0.InitStatus_SUCCESS, initResponse.GetState().GetState())

	base.DependencyEndpoints = nil
	initResponse, err = base.Builder.InitResponse()
	require.NoError(t, err)
	require.Equal(t, builderv0.InitStatus_SUCCESS, initResponse.GetState().GetState())
}

func TestRuntimeLoadServiceLoadsEnvironmentAndEndpoints(t *testing.T) {
	ctx := context.Background()
	identity := saveLoadTestService(t, ctx)
	base := NewServiceBase(ctx, &resources.Agent{Kind: resources.ServiceAgent, Name: "test", Version: "0.0.1"})
	resolved := false

	response, err := base.Runtime.LoadService(ctx, &runtimev0.LoadRequest{
		Identity:    identity,
		Environment: &basev0.Environment{Name: "test"},
	}, RuntimeLoad{
		Settings: &struct{}{},
		ResolveEndpoints: func(_ context.Context, endpoints []*basev0.Endpoint) error {
			resolved = true
			require.Empty(t, endpoints)
			return nil
		},
	})
	require.NoError(t, err)
	require.Equal(t, runtimev0.LoadStatus_READY, response.GetStatus().GetState())
	require.True(t, resolved)
	require.Equal(t, "test", base.Environment.GetName())
}

func TestRuntimeLoadServiceHonorsDisableCatch(t *testing.T) {
	ctx := context.Background()
	identity := saveLoadTestService(t, ctx)
	base := NewServiceBase(ctx, &resources.Agent{Kind: resources.ServiceAgent, Name: "test", Version: "0.0.1"})

	response, err := base.Runtime.LoadService(ctx, &runtimev0.LoadRequest{
		Identity:     identity,
		Environment:  &basev0.Environment{Name: "test"},
		DisableCatch: true,
	}, RuntimeLoad{Settings: &struct{}{}})
	require.NoError(t, err)
	require.Equal(t, runtimev0.LoadStatus_READY, response.GetStatus().GetState())

	sentinel := errors.New("sentinel panic")
	var recovered any
	func() {
		defer func() { recovered = recover() }()
		defer base.Wool.Catch()
		panic(sentinel)
	}()
	recoveredErr, ok := recovered.(error)
	require.True(t, ok, "expected disabled catch to propagate the panic, got %#v", recovered)
	require.ErrorIs(t, recoveredErr, sentinel)
}

func saveLoadTestService(t *testing.T, ctx context.Context) *basev0.ServiceIdentity {
	t.Helper()
	workspace := t.TempDir()
	relative := filepath.Join("module", "example-service")
	service := &resources.Service{Name: "example-service", Version: "1.2.3"}
	require.NoError(t, service.SaveAtDir(ctx, filepath.Join(workspace, relative)))
	return &basev0.ServiceIdentity{
		Workspace:           "workspace",
		Module:              "module",
		Name:                "example-service",
		Version:             "1.2.3",
		WorkspacePath:       workspace,
		RelativeToWorkspace: relative,
	}
}
