package services

import (
	"context"
	"testing"

	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/stretchr/testify/require"
)

func TestDefaultBuilderProvidesSuccessfulNoopLifecycle(t *testing.T) {
	wrapper := &BuilderWrapper{Base: &Base{loaded: true}}
	defaults := NewDefaultBuilder(wrapper)

	initResponse, err := defaults.Init(context.Background(), &builderv0.InitRequest{})
	require.NoError(t, err)
	require.Equal(t, builderv0.InitStatus_SUCCESS, initResponse.GetState().GetState())

	updateResponse, err := defaults.Update(context.Background(), &builderv0.UpdateRequest{})
	require.NoError(t, err)
	require.Equal(t, builderv0.UpdateStatus_SUCCESS, updateResponse.GetState().GetState())

	syncResponse, err := defaults.Sync(context.Background(), &builderv0.SyncRequest{})
	require.NoError(t, err)
	require.Equal(t, builderv0.SyncStatus_SUCCESS, syncResponse.GetState().GetState())

	buildResponse, err := defaults.Build(context.Background(), &builderv0.BuildRequest{})
	require.NoError(t, err)
	require.Equal(t, builderv0.BuildStatus_SUCCESS, buildResponse.GetState().GetState())
}

func TestDefaultBuilderReportsMissingWiring(t *testing.T) {
	response, err := (*DefaultBuilder)(nil).Build(context.Background(), &builderv0.BuildRequest{})
	require.NoError(t, err)
	require.Equal(t, builderv0.BuildStatus_ERROR, response.GetState().GetState())
	require.Contains(t, response.GetState().GetMessage(), "not wired")
}

func TestDefaultRuntimeProvidesInformation(t *testing.T) {
	wrapper := &RuntimeWrapper{StartStatus: &runtimev0.StartStatus{State: runtimev0.StartStatus_STARTED}}
	defaults := NewDefaultRuntime(wrapper)

	response, err := defaults.Information(context.Background(), &runtimev0.InformationRequest{})
	require.NoError(t, err)
	require.Equal(t, runtimev0.StartStatus_STARTED, response.GetStartStatus().GetState())
	require.Equal(t, runtimev0.DesiredState_NOOP, response.GetDesiredState().GetStage())
}
