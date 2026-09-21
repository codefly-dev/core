package manager

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/codefly-dev/core/artifactexecution"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	solutionv0 "github.com/codefly-dev/core/generated/go/codefly/services/solution/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/solution"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func buildAcquiredExecutor(t *testing.T, label string) (string, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "acquired")
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "build", "-ldflags=-buildid="+label, "-o", path, "../services/testdata/artifactexecutor")
	cmd.Env = append(os.Environ(), "GOWORK=off")
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", output)
	digest, _, err := executableIdentity(t.Context(), path)
	require.NoError(t, err)
	// Acquisition stores content, not a runnable installation.
	require.NoError(t, os.Chmod(path, 0600))
	return path, digest
}

func preparedExecutor(t *testing.T, digest, protocol string) *basev0.ArtifactExecution {
	t.Helper()
	identity := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte("selected inputs")))
	request, err := artifactexecution.Prepare(&basev0.ArtifactExecution{
		ContractVersion: artifactexecution.Contract, SelectionIdentity: identity,
		Target: "modules/app", Service: "api", Protocol: protocol, ExecutorDigest: digest,
		ConfigurationIdentity: "hmac-" + identity, BindingIdentity: identity,
		Inputs:  []*basev0.ArtifactExecutionInput{{Name: "package", Artifact: "package", Uri: "https://example.test/package", MediaType: "application/json", Digest: identity}},
		Outputs: []*basev0.ArtifactExecutionOutput{{Name: "rendered", MediaType: "application/json"}},
	})
	require.NoError(t, err)
	return request
}

func TestLoadArtifact(t *testing.T) {
	home := t.TempDir()
	t.Setenv(resources.CodeflyHomeEnv, home)
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	path, digest := buildAcquiredExecutor(t, "one")
	options := []LoadOption{WithoutSandbox(), WithoutPrincipal(), WithStartupTimeout(5 * time.Second), WithDialTimeout(5 * time.Second)}
	assertCleaned := func(t *testing.T, marker string) {
		t.Helper()
		data, err := os.ReadFile(marker)
		require.NoError(t, err)
		pid, err := strconv.Atoi(string(data))
		require.NoError(t, err)
		require.ErrorIs(t, syscall.Kill(pid, 0), syscall.ESRCH)
		snapshots, err := filepath.Glob(filepath.Join(tmp, "codefly-executor-*"))
		require.NoError(t, err)
		require.Empty(t, snapshots)
	}

	for _, protocol := range []string{artifactexecution.BuilderBuild, artifactexecution.BuilderRender, artifactexecution.SolutionRender} {
		t.Run(protocol, func(t *testing.T) {
			request := preparedExecutor(t, digest, protocol)
			conn, err := LoadArtifact(t.Context(), path, request, options...)
			require.NoError(t, err)
			t.Cleanup(conn.Close)
			require.Equal(t, digest, conn.ArtifactDigest())
			require.Equal(t, digest, conn.ProcessInfo().ArtifactDigest)
			require.NotEqual(t, path, conn.artifactPath)
			require.True(t, conn.group.Alive())
			unauthenticated, err := grpc.NewClient(conn.GRPCConn().Target(), grpc.WithTransportCredentials(insecure.NewCredentials()))
			require.NoError(t, err)
			defer unauthenticated.Close()
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			_, err = agentv0.NewAgentClient(unauthenticated).GetAgentInformation(ctx, &agentv0.AgentInformationRequest{})
			require.Equal(t, codes.Unauthenticated, status.Code(err))
			staging := t.TempDir()
			var receipt *basev0.ArtifactExecutionReceipt
			switch protocol {
			case artifactexecution.BuilderBuild:
				response, callErr := builderv0.NewBuilderClient(conn.GRPCConn()).Build(t.Context(), &builderv0.BuildRequest{Execution: request, OutputDirectory: staging})
				require.NoError(t, callErr)
				receipt = response.Execution
			case artifactexecution.BuilderRender:
				response, callErr := builderv0.NewBuilderClient(conn.GRPCConn()).Deploy(t.Context(), &builderv0.DeploymentRequest{Execution: request, OutputDirectory: staging})
				require.NoError(t, callErr)
				receipt = response.Execution
			case artifactexecution.SolutionRender:
				response, callErr := solution.NewClient(conn.GRPCConn()).Render(t.Context(), solution.CeilingRender(), &solutionv0.RenderRequest{Execution: request, Destination: staging, Context: &solutionv0.SolutionContext{Artifact: &solutionv0.SolutionArtifact{ArtifactDigest: digest}}})
				require.NoError(t, callErr)
				receipt = response.Execution
			}
			require.NoError(t, artifactexecution.CheckReceipt(request, receipt))
			content, err := os.ReadFile(filepath.Join(staging, "rendered.json"))
			require.NoError(t, err)
			require.Equal(t, fmt.Sprintf("sha256:%x", sha256.Sum256(content)), receipt.Outputs[0].Digest)
			conn.Close()
			require.False(t, conn.group.Alive())
			require.NoFileExists(t, conn.artifactPath)
			require.FileExists(t, path)
		})
	}

	t.Run("no ambient acquisition or mismatched bytes", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "started")
		opts := append(append([]LoadOption{}, options...), WithEnv("EXECUTOR_START_MARKER="+marker))
		request := preparedExecutor(t, digest, artifactexecution.BuilderBuild)
		_, err := LoadArtifact(t.Context(), path+"-missing", request, opts...)
		require.Error(t, err)
		wrong := preparedExecutor(t, request.BindingIdentity, artifactexecution.BuilderBuild)
		_, err = LoadArtifact(t.Context(), path, wrong, opts...)
		require.ErrorContains(t, err, "digest does not match")
		require.NoFileExists(t, marker)
		_, err = LoadArtifact(t.Context(), path, request)
		require.ErrorContains(t, err, "WithSandbox")
		_, err = LoadArtifact(t.Context(), path, request, WithoutSandbox())
		require.ErrorContains(t, err, "WithPrincipal")
		_, err = LoadArtifact(t.Context(), path, nil, opts...)
		require.Error(t, err)
		request.Identity = ""
		_, err = LoadArtifact(t.Context(), path, request, opts...)
		require.ErrorContains(t, err, "prepared execution identity")
	})

	t.Run("pre-spawn failure removes both private directories", func(t *testing.T) {
		opts := append(append([]LoadOption{}, options...), WithUDS(), WithScopedAuthSecret([]byte("short")))
		_, err := LoadArtifact(t.Context(), path, preparedExecutor(t, digest, artifactexecution.BuilderBuild), opts...)
		require.ErrorContains(t, err, "scoped-auth secret")
		for _, prefix := range []string{"codefly-executor-*", "codefly-uds-*"} {
			entries, err := filepath.Glob(filepath.Join(tmp, prefix))
			require.NoError(t, err)
			require.Empty(t, entries)
		}
	})

	for _, mode := range []string{"unsupported", "future", "missing-protocol", "future-protocol", "wrong-executor", "no-render", "legacy-handshake"} {
		t.Run(mode, func(t *testing.T) {
			marker := filepath.Join(t.TempDir(), "started")
			opts := append(append([]LoadOption{}, options...), WithEnv("EXECUTOR_TEST_MODE="+mode, "EXECUTOR_START_MARKER="+marker))
			conn, err := LoadArtifact(t.Context(), path, preparedExecutor(t, digest, artifactexecution.SolutionRender), opts...)
			if conn != nil {
				t.Cleanup(conn.Close)
			}
			require.Error(t, err)
			require.Nil(t, conn)
			assertCleaned(t, marker)
		})
	}

	t.Run("cancellation cleans snapshot and process", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "started")
		opts := append(append([]LoadOption{}, options...), WithEnv("EXECUTOR_TEST_MODE=no-handshake", "EXECUTOR_START_MARKER="+marker))
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		finished := make(chan error, 1)
		request := preparedExecutor(t, digest, artifactexecution.BuilderBuild)
		go func() {
			_, err := LoadArtifact(ctx, path, request, opts...)
			finished <- err
		}()
		require.Eventually(t, func() bool { _, err := os.Stat(marker); return err == nil }, 10*time.Second, 10*time.Millisecond)
		cancel()
		select {
		case err := <-finished:
			require.Error(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("cancelled loader did not terminate")
		}
		assertCleaned(t, marker)
	})

	t.Run("connection cannot dispatch a different selection", func(t *testing.T) {
		request := preparedExecutor(t, digest, artifactexecution.BuilderBuild)
		conn, err := LoadArtifact(t.Context(), path, request, options...)
		require.NoError(t, err)
		defer conn.Close()
		request.Target = "modules/sibling"
		request.Identity = ""
		request, err = artifactexecution.Prepare(request)
		require.NoError(t, err)
		_, err = builderv0.NewBuilderClient(conn.GRPCConn()).Build(t.Context(), &builderv0.BuildRequest{Execution: request, OutputDirectory: t.TempDir()})
		require.ErrorIs(t, err, ErrAgentAdmission)
		_, err = builderv0.NewBuilderClient(conn.GRPCConn()).Build(t.Context(), &builderv0.BuildRequest{OutputDirectory: t.TempDir()})
		require.ErrorIs(t, err, ErrAgentAdmission)
		_, err = builderv0.NewBuilderClient(conn.GRPCConn()).Deploy(t.Context(), &builderv0.DeploymentRequest{Execution: preparedExecutor(t, digest, artifactexecution.BuilderRender), OutputDirectory: t.TempDir()})
		require.ErrorIs(t, err, ErrAgentAdmission)
	})
}

func TestLoadArtifactIndependentReplacements(t *testing.T) {
	t.Setenv(resources.CodeflyHomeEnv, t.TempDir())
	installed := &resources.Agent{Kind: resources.ServiceAgent, Publisher: "example.test", Name: "executor", Version: "1.0.0"}
	installPath, err := installed.Path(t.Context())
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Dir(installPath), 0700))
	require.NoError(t, os.WriteFile(installPath, []byte("active installation"), 0600))
	first, digestA := buildAcquiredExecutor(t, "first")
	second, digestB := buildAcquiredExecutor(t, "second")
	require.NotEqual(t, digestA, digestB)
	requests := []*basev0.ArtifactExecution{preparedExecutor(t, digestA, artifactexecution.BuilderBuild), preparedExecutor(t, digestB, artifactexecution.BuilderBuild)}
	requests[1].Target, requests[1].Identity = "modules/sibling", ""
	requests[1], err = artifactexecution.Prepare(requests[1])
	require.NoError(t, err)
	paths := []string{first, second}
	conns := make([]*AgentConn, 2)
	errors := make([]error, 2)
	var group sync.WaitGroup
	for i := range paths {
		group.Go(func() {
			conns[i], errors[i] = LoadArtifact(t.Context(), paths[i], requests[i], WithoutSandbox(), WithoutPrincipal())
		})
	}
	group.Wait()
	for i := range conns {
		require.NoError(t, errors[i])
		t.Cleanup(conns[i].Close)
	}
	require.NotEqual(t, conns[0].ProcessInfo().PID, conns[1].ProcessInfo().PID)
	// Replacing the acquired file must not alter either running snapshot.
	content, err := os.ReadFile(second)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(first, content, 0600))
	for i, conn := range conns {
		staging := t.TempDir()
		response, err := builderv0.NewBuilderClient(conn.GRPCConn()).Build(t.Context(), &builderv0.BuildRequest{Execution: proto.Clone(requests[i]).(*basev0.ArtifactExecution), OutputDirectory: staging})
		require.NoError(t, err)
		require.NoError(t, artifactexecution.CheckReceipt(requests[i], response.Execution))
	}
	conns[0].Close()
	require.True(t, conns[1].group.Alive())
	require.FileExists(t, first)
	require.FileExists(t, second)
	_, err = LoadArtifact(t.Context(), first, requests[0], WithoutSandbox(), WithoutPrincipal())
	require.ErrorContains(t, err, "digest does not match")
	installedContent, err := os.ReadFile(installPath)
	require.NoError(t, err)
	require.Equal(t, "active installation", string(installedContent))
}

func TestStageExecutableRejectsInvalidOrInterruptedAcquisition(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)
	path := filepath.Join(t.TempDir(), "acquired")
	content := []byte("complete acquisition")
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	require.NoError(t, os.WriteFile(path, content[:5], 0600))
	_, _, err := stageExecutable(t.Context(), path, digest)
	require.ErrorContains(t, err, "digest does not match")
	require.NoError(t, os.WriteFile(path, content, 0600))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, _, err = stageExecutable(ctx, path, digest)
	require.ErrorIs(t, err, context.Canceled)
	_, _, err = stageExecutable(t.Context(), filepath.Dir(path), digest)
	require.ErrorContains(t, err, "regular file")
	fifo := filepath.Join(t.TempDir(), "pipe")
	require.NoError(t, syscall.Mkfifo(fifo, 0600))
	_, _, err = stageExecutable(t.Context(), fifo, digest)
	require.ErrorContains(t, err, "regular file")
	entries, err := os.ReadDir(tmp)
	require.NoError(t, err)
	require.Empty(t, entries)
}
