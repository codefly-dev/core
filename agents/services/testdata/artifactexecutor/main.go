package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"

	"github.com/codefly-dev/core/artifactexecution"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	solutionv0 "github.com/codefly-dev/core/generated/go/codefly/services/solution/v0"
	"google.golang.org/grpc"
)

type executor struct {
	mode   string
	digest string
}

type builderExecutor struct {
	builderv0.UnimplementedBuilderServer
	*executor
}
type solutionExecutor struct {
	solutionv0.UnimplementedSolutionServer
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

func (s *builderExecutor) BuildCapabilities(context.Context, *builderv0.BuildCapabilitiesRequest) (*builderv0.BuildCapabilitiesResponse, error) {
	return &builderv0.BuildCapabilitiesResponse{ExecutionContracts: s.contracts()}, nil
}

func (s *solutionExecutor) GetSolutionInformation(_ context.Context, request *solutionv0.GetSolutionInformationRequest) (*solutionv0.GetSolutionInformationResponse, error) {
	return &solutionv0.GetSolutionInformationResponse{Artifact: &solutionv0.SolutionArtifact{Publisher: request.GetArtifact().GetPublisher(), Name: request.GetArtifact().GetName(), Version: request.GetArtifact().GetVersion(), ArtifactDigest: s.digest}, Capabilities: &solutionv0.SolutionCapabilities{SupportsRender: true, ExecutionContracts: s.contracts()}}, nil
}

func (s *executor) emit(execution *basev0.ArtifactExecution, protocol, directory string) (*basev0.ArtifactExecutionReceipt, error) {
	if err := artifactexecution.Check(execution, protocol, s.digest, s.contracts()); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(directory, "dispatched"), []byte(protocol), 0600); err != nil {
		return nil, err
	}
	receipt := &basev0.ArtifactExecutionReceipt{Identity: execution.Identity}
	for _, output := range execution.Outputs {
		content, err := json.Marshal(struct {
			Selection string                           `json:"selection"`
			Inputs    []*basev0.ArtifactExecutionInput `json:"inputs"`
		}{execution.SelectionIdentity, execution.Inputs})
		if err != nil {
			return nil, err
		}
		path := output.Name + ".json"
		if err := os.WriteFile(filepath.Join(directory, path), content, 0600); err != nil {
			return nil, err
		}
		receipt.Outputs = append(receipt.Outputs, &basev0.ArtifactExecutionOutput{Name: output.Name, MediaType: output.MediaType, Path: path, Digest: fmt.Sprintf("sha256:%x", sha256.Sum256(content))})
	}
	if s.mode == "wrong-ack" {
		receipt.Identity = s.digest
	}
	if s.mode == "missing-ack" {
		return nil, nil
	}
	return receipt, nil
}

func (s *builderExecutor) Build(_ context.Context, request *builderv0.BuildRequest) (*builderv0.BuildResponse, error) {
	receipt, err := s.emit(request.Execution, artifactexecution.BuilderBuild, request.OutputDirectory)
	state := builderv0.BuildStatus_SUCCESS
	if s.mode == "failed" {
		state = builderv0.BuildStatus_ERROR
	}
	return &builderv0.BuildResponse{State: &builderv0.BuildStatus{State: state}, Execution: receipt}, err
}

func (s *builderExecutor) Deploy(_ context.Context, request *builderv0.DeploymentRequest) (*builderv0.DeploymentResponse, error) {
	receipt, err := s.emit(request.Execution, artifactexecution.BuilderRender, request.OutputDirectory)
	state := builderv0.DeploymentStatus_SUCCESS
	if s.mode == "failed" {
		state = builderv0.DeploymentStatus_ERROR
	}
	return &builderv0.DeploymentResponse{State: &builderv0.DeploymentStatus{State: state}, Execution: receipt}, err
}

func (s *solutionExecutor) Render(_ context.Context, request *solutionv0.RenderRequest) (*solutionv0.RenderResponse, error) {
	receipt, err := s.emit(request.Execution, artifactexecution.SolutionRender, request.Destination)
	return &solutionv0.RenderResponse{Execution: receipt}, err
}

func main() {
	mode := flag.String("mode", "supported", "operation contract mode")
	address := flag.String("address-file", "", "write listening address here")
	flag.Parse()
	path, err := os.Executable()
	if err != nil {
		panic(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		panic(err)
	}
	s := &executor{mode: *mode, digest: fmt.Sprintf("sha256:%x", sha256.Sum256(data))}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	server := grpc.NewServer()
	if *mode == "legacy" {
		descriptor := builderv0.Builder_ServiceDesc
		descriptor.Methods = nil
		for _, method := range builderv0.Builder_ServiceDesc.Methods {
			if method.MethodName != "BuildCapabilities" {
				descriptor.Methods = append(descriptor.Methods, method)
			}
		}
		server.RegisterService(&descriptor, &builderExecutor{executor: s})
	} else {
		builderv0.RegisterBuilderServer(server, &builderExecutor{executor: s})
	}
	solutionv0.RegisterSolutionServer(server, &solutionExecutor{executor: s})
	if err := os.WriteFile(*address, []byte(listener.Addr().String()), 0600); err != nil {
		panic(err)
	}
	if err := server.Serve(listener); err != nil {
		panic(err)
	}
}
