package services

import (
	"context"
	"testing"

	builderv1 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v1"
	runtimev1 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v1"
	"github.com/stretchr/testify/require"
)

func TestDefaultBuilderProvidesSuccessfulNoopLifecycle(t *testing.T) {
	wrapper := &BuilderWrapper{Base: &Base{loaded: true}}
	defaults := NewDefaultBuilder(wrapper)

	initResponse, err := defaults.Init(context.Background(), &builderv1.InitRequest{})
	require.NoError(t, err)
	require.Equal(t, builderv1.InitStatus_SUCCESS, initResponse.GetState().GetState())

	updateResponse, err := defaults.Update(context.Background(), &builderv1.UpdateRequest{})
	require.NoError(t, err)
	require.Equal(t, builderv1.UpdateStatus_SUCCESS, updateResponse.GetState().GetState())

	syncResponse, err := defaults.Sync(context.Background(), &builderv1.SyncRequest{})
	require.NoError(t, err)
	require.Equal(t, builderv1.SyncStatus_SUCCESS, syncResponse.GetState().GetState())

	dryRunResponse, err := defaults.Sync(context.Background(), &builderv1.SyncRequest{DryRun: true})
	require.NoError(t, err)
	require.Equal(t, builderv1.SyncStatus_UNSUPPORTED, dryRunResponse.GetState().GetState())

	buildResponse, err := defaults.Build(context.Background(), &builderv1.BuildRequest{})
	require.NoError(t, err)
	require.Equal(t, builderv1.BuildStatus_SUCCESS, buildResponse.GetState().GetState())
}

func TestDefaultBuilderReportsMissingWiring(t *testing.T) {
	response, err := (*DefaultBuilder)(nil).Build(context.Background(), &builderv1.BuildRequest{})
	require.NoError(t, err)
	require.Equal(t, builderv1.BuildStatus_ERROR, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "not wired")
}

func TestDefaultRuntimeProvidesInformation(t *testing.T) {
	wrapper := &RuntimeWrapper{StartStatus: &runtimev1.StartStatus{State: runtimev1.StartStatus_STARTED}}
	defaults := NewDefaultRuntime(wrapper)

	response, err := defaults.Information(context.Background(), &runtimev1.InformationRequest{})
	require.NoError(t, err)
	require.Equal(t, runtimev1.StartStatus_STARTED, response.GetStartStatus().GetState())
	require.Equal(t, runtimev1.DesiredState_NOOP, response.GetDesiredState().GetStage())
}
