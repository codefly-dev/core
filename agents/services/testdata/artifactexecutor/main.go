package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/codefly-dev/core/agents"
	"github.com/codefly-dev/core/agents/contract"
	"github.com/codefly-dev/core/artifactexecution"
	basev1 "github.com/codefly-dev/core/generated/go/codefly/base/v1"
	agentv1 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v1"
	builderv1 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v1"
	solutionv1 "github.com/codefly-dev/core/generated/go/codefly/services/solution/v1"
	"google.golang.org/grpc"
)

type executor struct {
	mode   string
	digest string
}

type agentExecutor struct {
	agentv1.UnimplementedAgentServer
	*executor
}

func (s *agentExecutor) GetAgentInformation(context.Context, *agentv1.AgentInformationRequest) (*agentv1.AgentInformation, error) {
	declaration := contract.Current()
	if s.mode == "missing-protocol" {
		declaration = &agentv1.AgentContract{}
	} else if s.mode == "future-protocol" {
		declaration.ProtocolVersion++
	}
	return &agentv1.AgentInformation{Contract: declaration}, nil
}

type builderExecutor struct {
	builderv1.UnimplementedBuilderServer
	*executor
}
type solutionExecutor struct {
	solutionv1.UnimplementedSolutionServer
	*executor
}

func (s *executor) contracts() []string {
	if s.mode == "unsupported" {
		return nil
	}
	if s.mode == "future" {
		return []string{"artifact-execution/v2"}
	}
	return []string{artifactexecution.Contract}
}

func (s *builderExecutor) BuildCapabilities(context.Context, *builderv1.BuildCapabilitiesRequest) (*builderv1.BuildCapabilitiesResponse, error) {
	return &builderv1.BuildCapabilitiesResponse{ExecutionContracts: s.contracts()}, nil
}

func (s *solutionExecutor) GetSolutionInformation(_ context.Context, request *solutionv1.GetSolutionInformationRequest) (*solutionv1.GetSolutionInformationResponse, error) {
	digest := s.digest
	if s.mode == "wrong-executor" {
		digest = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
	}
	return &solutionv1.GetSolutionInformationResponse{Artifact: &solutionv1.SolutionArtifact{Publisher: request.GetArtifact().GetPublisher(), Name: request.GetArtifact().GetName(), Version: request.GetArtifact().GetVersion(), ArtifactDigest: digest}, Capabilities: &solutionv1.SolutionCapabilities{SupportsRender: s.mode != "no-render", ExecutionContracts: s.contracts()}}, nil
}

func (s *executor) emit(execution *basev1.ArtifactExecution, protocol, directory string) (*basev1.ArtifactExecutionReceipt, error) {
	if err := artifactexecution.Check(execution, protocol, s.digest, s.contracts()); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(directory, "dispatched"), []byte(protocol), 0600); err != nil {
		return nil, err
	}
	receipt := &basev1.ArtifactExecutionReceipt{Identity: execution.Identity}
	for _, output := range execution.Outputs {
		content, err := json.Marshal(struct {
			Selection string                           `json:"selection"`
			Inputs    []*basev1.ArtifactExecutionInput `json:"inputs"`
		}{execution.SelectionIdentity, execution.Inputs})
		if err != nil {
			return nil, err
		}
		path := output.Name + ".json"
		if err := os.WriteFile(filepath.Join(directory, path), content, 0600); err != nil {
			return nil, err
		}
		receipt.Outputs = append(receipt.Outputs, &basev1.ArtifactExecutionOutput{Name: output.Name, MediaType: output.MediaType, Path: path, Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(content))})
	}
	if s.mode == "wrong-ack" {
		receipt.Identity = s.digest
	}
	if s.mode == "missing-ack" {
		return nil, nil
	}
	if s.mode == "child-writer" {
		path, err := os.Executable()
		if err != nil {
			return nil, err
		}
		child := exec.Command(path)
		child.Env = append(os.Environ(), "EXECUTOR_CHILD_OUTPUT="+filepath.Join(directory, execution.Outputs[0].Name+".json"))
		if err := child.Start(); err != nil {
			return nil, err
		}
		go func() { _ = child.Wait() }()
	}
	return receipt, nil
}

func (s *builderExecutor) Build(_ context.Context, request *builderv1.BuildRequest) (*builderv1.BuildResponse, error) {
	receipt, err := s.emit(request.Execution, artifactexecution.BuilderBuild, request.OutputDirectory)
	state := builderv1.BuildStatus_SUCCESS
	if s.mode == "failed" {
		state = builderv1.BuildStatus_ERROR
	}
	return &builderv1.BuildResponse{State: &builderv1.BuildStatus{State: state}, Execution: receipt}, err
}

func (s *builderExecutor) Deploy(_ context.Context, request *builderv1.DeploymentRequest) (*builderv1.DeploymentResponse, error) {
	receipt, err := s.emit(request.Execution, artifactexecution.BuilderRender, request.OutputDirectory)
	state := builderv1.DeploymentStatus_SUCCESS
	if s.mode == "failed" {
		state = builderv1.DeploymentStatus_ERROR
	}
	return &builderv1.DeploymentResponse{State: &builderv1.DeploymentStatus{State: state}, Execution: receipt}, err
}

func (s *solutionExecutor) Render(_ context.Context, request *solutionv1.RenderRequest) (*solutionv1.RenderResponse, error) {
	receipt, err := s.emit(request.Execution, artifactexecution.SolutionRender, request.Destination)
	return &solutionv1.RenderResponse{Execution: receipt}, err
}

func main() {
	if output := os.Getenv("EXECUTOR_CHILD_OUTPUT"); output != "" {
		time.Sleep(500 * time.Millisecond)
		if err := os.WriteFile(output, []byte("changed by surviving executor child"), 0600); err != nil {
			panic(err)
		}
		time.Sleep(time.Minute)
		return
	}
	mode := flag.String("mode", "supported", "operation contract mode")
	address := flag.String("address-file", "", "write listening address here")
	flag.Parse()
	if value := os.Getenv("EXECUTOR_TEST_MODE"); value != "" {
		*mode = value
	}
	if marker := os.Getenv("EXECUTOR_START_MARKER"); marker != "" {
		// Written through a rename, so the marker's EXISTENCE means its content
		// is complete. A plain os.WriteFile creates the file and then writes it,
		// and every reader waits on os.Stat — so an empty marker is observable,
		// and a reader that cancels this process in that window gets a file that
		// never receives the pid at all. Measured: strconv.Atoi("") in
		// TestLoadArtifact/cancellation_cleans_snapshot_and_process.
		partial := marker + ".partial"
		if err := os.WriteFile(partial, []byte(fmt.Sprint(os.Getpid())), 0600); err != nil {
			panic(err)
		}
		if err := os.Rename(partial, marker); err != nil {
			panic(err)
		}
	}
	if *mode == "no-handshake" {
		time.Sleep(time.Minute)
		return
	}
	if *mode == "legacy-handshake" {
		fmt.Println("1|127.0.0.1:1")
		time.Sleep(time.Minute)
		return
	}
	path, err := os.Executable()
	if err != nil {
		panic(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	s := &executor{mode: *mode, digest: fmt.Sprintf("sha256:%x", sha256.Sum256(data))}
	if *address == "" {
		agents.Serve(agents.PluginRegistration{Agent: &agentExecutor{executor: s}, Builder: &builderExecutor{executor: s}, Solution: &solutionExecutor{executor: s}})
		return
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	server := grpc.NewServer()
	if *mode == "legacy" {
		descriptor := builderv1.Builder_ServiceDesc
		descriptor.Methods = nil
		for _, method := range builderv1.Builder_ServiceDesc.Methods {
			if method.MethodName != "BuildCapabilities" {
				descriptor.Methods = append(descriptor.Methods, method)
			}
		}
		server.RegisterService(&descriptor, &builderExecutor{executor: s})
	} else {
		builderv1.RegisterBuilderServer(server, &builderExecutor{executor: s})
	}
	solutionv1.RegisterSolutionServer(server, &solutionExecutor{executor: s})
	if err := os.WriteFile(*address, []byte(listener.Addr().String()), 0600); err != nil {
		panic(err)
	}
	if err := server.Serve(listener); err != nil {
		panic(err)
	}
}
