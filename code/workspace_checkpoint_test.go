package code

import (
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"testing"

	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	codev0 "github.com/codefly-dev/core/generated/go/codefly/services/code/v0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/proto"
)

func TestWorkspaceCheckpointAgentIntegration(t *testing.T) {
	root := t.TempDir()
	writeSourceManifestFile(t, root, "go.mod", "module checkpoint.test\n\ngo 1.25\n", 0o644)
	writeSourceManifestFile(t, root, "candidate.go", "package candidate\n\nfunc Value() int { return 0 }\n", 0o644)
	gitSourceManifest(t, root, "init", "-b", "main")
	gitSourceManifest(t, root, "config", "commit.gpgsign", "false")
	gitSourceManifest(t, root, "add", "go.mod", "candidate.go")
	gitSourceManifest(t, root, "commit", "-m", "baseline")

	writeSourceManifestFile(t, root, "candidate.go", "package candidate\n\nfunc Value() int { return 1 }\n", 0o644)
	writeSourceManifestFile(t, root, "candidate_test.go", "package candidate\n\nimport \"testing\"\n\nfunc TestValue(t *testing.T) { if Value() != 1 { t.Fatalf(\"Value() = %d\", Value()) } }\n", 0o644)
	writeSourceManifestFile(t, root, ".pytest_cache/state", "candidate-cache\n", 0o644)

	server := NewDefaultCodeServer(root)
	_, candidateState, err := server.currentWorkspaceCheckpointState(t.Context())
	if err != nil {
		t.Fatalf("candidate workspace state: %v", err)
	}
	client, closeClient := startWorkspaceCheckpointCodeClient(t, server)
	lease := &basev0.WorkspaceLeaseIdentity{WorkspaceId: "workspace-319", LeaseId: "lease-primary"}
	createRequest := &codev0.CodeRequest{Operation: &codev0.CodeRequest_CreateWorkspaceCheckpoint{CreateWorkspaceCheckpoint: &basev0.CreateWorkspaceCheckpointRequest{
		Lease: lease, ExpectedWorkspaceVersion: candidateState.GetVersion(), CallerId: "mind-attempt-1", IdempotencyKey: "create-candidate-1",
		RetentionIntent: basev0.WorkspaceCheckpointRetentionIntent_WORKSPACE_CHECKPOINT_RETENTION_INTENT_UNTIL_RELEASE_OR_LEASE_END,
	}}}
	createdEnvelope, err := client.Execute(t.Context(), createRequest)
	if err != nil {
		t.Fatalf("CreateWorkspaceCheckpoint: %v", err)
	}
	created := createdEnvelope.GetCreateWorkspaceCheckpoint()
	if created.GetFailure() != nil || created.GetCheckpoint() == nil {
		t.Fatalf("CreateWorkspaceCheckpoint response = %+v", created)
	}
	checkpoint := created.GetCheckpoint()
	if checkpoint.GetSource().GetVersion() != candidateState.GetVersion() || checkpoint.GetSource().GetDigest() != candidateState.GetDigest() {
		t.Fatalf("checkpoint source = %+v, want %+v", checkpoint.GetSource(), candidateState)
	}
	if checkpoint.GetStorageRequirement().GetBytes() == 0 || !checkpoint.GetStorageAdmission().GetAdmitted() || checkpoint.GetCreatedAt() == nil {
		t.Fatalf("checkpoint receipt is incomplete: %+v", checkpoint)
	}
	retriedEnvelope, err := client.Execute(t.Context(), createRequest)
	if err != nil || retriedEnvelope.GetFailure() != nil {
		t.Fatalf("idempotent CreateWorkspaceCheckpoint: response=%+v err=%v", retriedEnvelope, err)
	}
	if !proto.Equal(checkpoint, retriedEnvelope.GetCreateWorkspaceCheckpoint().GetCheckpoint()) {
		t.Fatalf("idempotent create returned another receipt")
	}
	closeClient()

	server = NewDefaultCodeServer(root)
	client, closeClient = startWorkspaceCheckpointCodeClient(t, server)
	defer closeClient()
	writeResponse, err := client.Execute(t.Context(), &codev0.CodeRequest{Operation: &codev0.CodeRequest_WriteFile{WriteFile: &codev0.WriteFileRequest{
		Path: "candidate.go", Content: "package candidate\n\nfunc Value() int { return 2 }\n",
	}}})
	if err != nil || !writeResponse.GetWriteFile().GetSuccess() {
		t.Fatalf("mutate candidate: response=%+v err=%v", writeResponse, err)
	}
	writeSourceManifestFile(t, root, "regression.txt", "later mutation\n", 0o644)
	writeSourceManifestFile(t, root, ".pytest_cache/state", "later-cache\n", 0o644)
	_, regressedState, err := server.currentWorkspaceCheckpointState(t.Context())
	if err != nil {
		t.Fatalf("regressed workspace state: %v", err)
	}

	stale := restoreWorkspaceCheckpointRequest(checkpoint.GetCheckpointId(), lease, candidateState.GetVersion(), "restore-stale")
	staleEnvelope, err := client.Execute(t.Context(), stale)
	if err != nil || staleEnvelope.GetFailure().GetCode() != basev0.FailureCode_FAILURE_CODE_PRECONDITION_FAILED {
		t.Fatalf("stale restore = response:%+v err:%v", staleEnvelope, err)
	}
	if body, err := os.ReadFile(filepath.Join(root, "candidate.go")); err != nil || string(body) != "package candidate\n\nfunc Value() int { return 2 }\n" {
		t.Fatalf("stale restore changed candidate: body=%q err=%v", body, err)
	}

	crossLease := restoreWorkspaceCheckpointRequest(checkpoint.GetCheckpointId(), &basev0.WorkspaceLeaseIdentity{WorkspaceId: lease.GetWorkspaceId(), LeaseId: "lease-other"}, regressedState.GetVersion(), "restore-cross-lease")
	crossLeaseEnvelope, err := client.Execute(t.Context(), crossLease)
	if err != nil || crossLeaseEnvelope.GetFailure().GetCode() != basev0.FailureCode_FAILURE_CODE_PERMISSION_DENIED {
		t.Fatalf("cross-lease restore = response:%+v err:%v", crossLeaseEnvelope, err)
	}

	cancelledContext, cancel := context.WithCancel(t.Context())
	cancel()
	cancelled, err := server.Execute(cancelledContext, &codev0.CodeRequest{Operation: &codev0.CodeRequest_CreateWorkspaceCheckpoint{CreateWorkspaceCheckpoint: &basev0.CreateWorkspaceCheckpointRequest{
		Lease: lease, ExpectedWorkspaceVersion: regressedState.GetVersion(), CallerId: "mind-attempt-1", IdempotencyKey: "cancelled-create",
		RetentionIntent: basev0.WorkspaceCheckpointRetentionIntent_WORKSPACE_CHECKPOINT_RETENTION_INTENT_UNTIL_RELEASE_OR_LEASE_END,
	}}})
	if err != nil || cancelled.GetFailure().GetCode() != basev0.FailureCode_FAILURE_CODE_CANCELLED {
		t.Fatalf("cancelled create = response:%+v err:%v", cancelled, err)
	}
	cancelledRestore, err := server.Execute(cancelledContext, restoreWorkspaceCheckpointRequest(checkpoint.GetCheckpointId(), lease, regressedState.GetVersion(), "cancelled-restore"))
	if err != nil || cancelledRestore.GetFailure().GetCode() != basev0.FailureCode_FAILURE_CODE_CANCELLED {
		t.Fatalf("cancelled restore = response:%+v err:%v", cancelledRestore, err)
	}
	if body, err := os.ReadFile(filepath.Join(root, "candidate.go")); err != nil || string(body) != "package candidate\n\nfunc Value() int { return 2 }\n" {
		t.Fatalf("cancelled restore changed candidate: body=%q err=%v", body, err)
	}

	restoreRequest := restoreWorkspaceCheckpointRequest(checkpoint.GetCheckpointId(), lease, regressedState.GetVersion(), "restore-candidate")
	restoredEnvelope, err := client.Execute(t.Context(), restoreRequest)
	if err != nil {
		t.Fatalf("RestoreWorkspaceCheckpoint: %v", err)
	}
	restored := restoredEnvelope.GetRestoreWorkspaceCheckpoint()
	if restored.GetFailure() != nil || restored.GetWorkspace().GetVersion() != candidateState.GetVersion() {
		t.Fatalf("RestoreWorkspaceCheckpoint response = %+v", restored)
	}
	if restored.GetReceipt().GetPrior().GetVersion() != regressedState.GetVersion() || restored.GetReceipt().GetRestoredAt() == nil {
		t.Fatalf("restore receipt = %+v", restored.GetReceipt())
	}
	verified, err := client.Execute(t.Context(), &codev0.CodeRequest{Operation: &codev0.CodeRequest_ShellExec{ShellExec: &codev0.ShellExecRequest{
		Args: []string{"go", "test", "./..."}, TimeoutSeconds: 30,
	}}})
	if err != nil || verified.GetFailure() != nil || verified.GetShellExec().GetExitCode() != 0 {
		t.Fatalf("runtime verification after restore = response:%+v err:%v", verified, err)
	}
	if _, err := os.Stat(filepath.Join(root, "regression.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("later untracked file survived restore: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, ".pytest_cache")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("runtime cache survived restore: %v", err)
	}
	gitSourceManifest(t, root, "status", "--short")

	retriedRestore, err := client.Execute(t.Context(), restoreRequest)
	if err != nil || retriedRestore.GetFailure() != nil || !proto.Equal(restored.GetReceipt(), retriedRestore.GetRestoreWorkspaceCheckpoint().GetReceipt()) {
		t.Fatalf("idempotent restore = response:%+v err:%v", retriedRestore, err)
	}

	releaseRequest := &codev0.CodeRequest{Operation: &codev0.CodeRequest_ReleaseWorkspaceCheckpoint{ReleaseWorkspaceCheckpoint: &basev0.ReleaseWorkspaceCheckpointRequest{
		CheckpointId: checkpoint.GetCheckpointId(), Lease: lease, CallerId: "mind-attempt-1", IdempotencyKey: "release-candidate",
	}}}
	releasedEnvelope, err := client.Execute(t.Context(), releaseRequest)
	if err != nil || releasedEnvelope.GetFailure() != nil || releasedEnvelope.GetReleaseWorkspaceCheckpoint().GetReceipt().GetReleasedAt() == nil {
		t.Fatalf("ReleaseWorkspaceCheckpoint = response:%+v err:%v", releasedEnvelope, err)
	}
	retriedRelease, err := client.Execute(t.Context(), releaseRequest)
	if err != nil || retriedRelease.GetFailure() != nil || !proto.Equal(releasedEnvelope.GetReleaseWorkspaceCheckpoint().GetReceipt(), retriedRelease.GetReleaseWorkspaceCheckpoint().GetReceipt()) {
		t.Fatalf("idempotent release = response:%+v err:%v", retriedRelease, err)
	}
	store, failure := server.workspaceCheckpointStoreDir()
	if failure != nil {
		t.Fatalf("checkpoint store: %v", failure)
	}
	if _, err := os.Stat(workspaceCheckpointArchivePath(store, checkpoint.GetCheckpointId())); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("released archive still exists: %v", err)
	}
}

func restoreWorkspaceCheckpointRequest(checkpointID string, lease *basev0.WorkspaceLeaseIdentity, expectedVersion, idempotencyKey string) *codev0.CodeRequest {
	return &codev0.CodeRequest{Operation: &codev0.CodeRequest_RestoreWorkspaceCheckpoint{RestoreWorkspaceCheckpoint: &basev0.RestoreWorkspaceCheckpointRequest{
		CheckpointId: checkpointID, Lease: lease, ExpectedWorkspaceVersion: expectedVersion,
		AuthorizationId: "authorization-319", IdempotencyKey: idempotencyKey,
	}}}
}

func startWorkspaceCheckpointCodeClient(t *testing.T, server codev0.CodeServer) (codev0.CodeClient, func()) {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	grpcServer := grpc.NewServer()
	codev0.RegisterCodeServer(grpcServer, server)
	go func() {
		_ = grpcServer.Serve(listener)
	}()
	connection, err := grpc.NewClient("passthrough:///workspace-checkpoint", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
		return listener.Dial()
	}))
	if err != nil {
		grpcServer.Stop()
		_ = listener.Close()
		t.Fatalf("connect Code client: %v", err)
	}
	return codev0.NewCodeClient(connection), func() {
		_ = connection.Close()
		grpcServer.Stop()
		_ = listener.Close()
	}
}
