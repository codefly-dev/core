package services

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/codefly-dev/core/agents/manager"
	"github.com/codefly-dev/core/artifactexecution"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	solutionv0 "github.com/codefly-dev/core/generated/go/codefly/services/solution/v0"
	"github.com/codefly-dev/core/solution"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestArtifactExecutionOverIndependentProcess(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	binary := filepath.Join(t.TempDir(), "executor")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./testdata/artifactexecutor")
	build.Env = append(os.Environ(), "GOWORK=off")
	output, err := build.CombinedOutput()
	require.NoError(t, err, "%s", output)
	content, err := os.ReadFile(binary)
	require.NoError(t, err)
	executorDigest := fmt.Sprintf("sha256:%x", sha256.Sum256(content))
	for _, mode := range []string{"supported", "unsupported", "future", "legacy", "wrong-ack", "missing-ack", "failed", "wrong-executor", "missing-process", "wrong-protocol", "ambiguous-solution-input", "no-staging"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			addressFile := filepath.Join(t.TempDir(), "address")
			command := exec.CommandContext(ctx, binary, "--mode", mode, "--address-file", addressFile)
			command.Stderr = os.Stderr
			require.NoError(t, command.Start())
			t.Cleanup(func() { cancel(); _ = command.Wait() })
			var address []byte
			require.Eventually(t, func() bool { address, err = os.ReadFile(addressFile); return err == nil && len(address) != 0 }, 5*time.Second, 10*time.Millisecond)
			conn, err := grpc.NewClient(string(address), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithUnaryInterceptor(solution.EnforcingClientInterceptor()))
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, conn.Close()) })
			builder := NewBuilderAgentClient(conn)
			builder.ProcessInfo = &manager.ProcessInfo{ArtifactDigest: executorDigest}
			if mode == "missing-process" {
				builder.ProcessInfo = nil
			}
			for _, protocol := range []string{artifactexecution.BuilderBuild, artifactexecution.BuilderRender, artifactexecution.SolutionRender} {
				if protocol == artifactexecution.SolutionRender && (mode == "legacy" || mode == "missing-process" || mode == "failed") {
					continue
				}
				if mode == "ambiguous-solution-input" && protocol != artifactexecution.SolutionRender {
					continue
				}
				digest := "sha256:" + strings.Repeat("a", 64)
				request := &basev0.ArtifactExecution{ContractVersion: artifactexecution.Contract, SelectionIdentity: digest, Target: "modules/app", Service: "api", Protocol: protocol, ExecutorDigest: executorDigest, ConfigurationIdentity: "hmac-" + digest, BindingIdentity: digest,
					Inputs: []*basev0.ArtifactExecutionInput{{Name: "runtime", Target: "modules/app", Artifact: "runtime", Uri: "https://artifacts.example.test/runtime", MediaType: "application/octet-stream", Digest: digest}}, Outputs: []*basev0.ArtifactExecutionOutput{{Name: "bundle", MediaType: "application/json"}}}
				if mode == "wrong-executor" {
					request.ExecutorDigest = digest
				}
				if mode == "wrong-protocol" {
					request.Protocol = artifactexecution.SolutionRender
					if protocol == artifactexecution.SolutionRender {
						request.Protocol = artifactexecution.BuilderBuild
					}
				}
				request, err = artifactexecution.Prepare(request)
				require.NoError(t, err)
				directory := t.TempDir()
				staging := directory
				if mode == "no-staging" {
					staging = ""
				}
				var receipt *basev0.ArtifactExecutionReceipt
				switch protocol {
				case artifactexecution.BuilderBuild:
					response, callErr := builder.Build(ctx, &builderv0.BuildRequest{Execution: request, OutputDirectory: staging})
					err, receipt = callErr, response.GetExecution()
				case artifactexecution.BuilderRender:
					response, callErr := builder.Deploy(ctx, &builderv0.DeploymentRequest{Execution: request, OutputDirectory: staging})
					err, receipt = callErr, response.GetExecution()
				case artifactexecution.SolutionRender:
					render := &solutionv0.RenderRequest{Context: &solutionv0.SolutionContext{Artifact: &solutionv0.SolutionArtifact{Publisher: "example", Name: "executor", Version: "999.0.0", ArtifactDigest: executorDigest}}, Execution: request, Destination: staging}
					if mode == "ambiguous-solution-input" {
						render.ArtifactReference = "oci://other.example.test/image"
					}
					response, callErr := solution.NewClient(conn).Render(ctx, solution.CeilingRender(), render)
					err, receipt = callErr, response.GetExecution()
				}
				if mode != "supported" {
					require.Error(t, err, protocol)
				} else {
					require.NoError(t, err, protocol)
					require.Len(t, receipt.Outputs, 1)
					bytes, err := os.ReadFile(filepath.Join(directory, receipt.Outputs[0].Path))
					require.NoError(t, err)
					require.Equal(t, fmt.Sprintf("sha256:%x", sha256.Sum256(bytes)), receipt.Outputs[0].Digest)
					require.Contains(t, string(bytes), "modules/app")
				}
				if mode == "supported" || mode == "wrong-ack" || mode == "missing-ack" || mode == "failed" {
					require.FileExists(t, filepath.Join(directory, "dispatched"))
				} else {
					require.NoFileExists(t, filepath.Join(directory, "dispatched"), "refusal must precede side effects")
				}
			}
		})
	}
}
