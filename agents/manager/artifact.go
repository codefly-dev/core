package manager

import (
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/codefly-dev/core/agents/contract"
	"github.com/codefly-dev/core/artifactexecution"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	agentv0 "github.com/codefly-dev/core/generated/go/codefly/services/agent/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	solutionv0 "github.com/codefly-dev/core/generated/go/codefly/services/solution/v0"
	"github.com/codefly-dev/core/solution"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/proto"
)

// LoadArtifact loads an acquired native executable for one prepared execution.
// The expected digest identifies executable bytes, not an archive or OCI manifest.
// Release authority and acquisition are checked by composition before this call;
// loading proves content identity and live compatibility, not deployment authority.
// No installed agent is resolved, downloaded, replaced, or removed.
func LoadArtifact(ctx context.Context, path string, execution *basev0.ArtifactExecution, opts ...LoadOption) (*AgentConn, error) {
	request, err := artifactexecution.Prepare(execution)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAgentAdmission, err)
	}
	if execution.Identity == "" || !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%w: prepared execution identity and absolute executable path required", ErrAgentAdmission)
	}
	cfg, err := configureLoad(opts)
	if err != nil {
		return nil, err
	}
	bin, dir, err := stageExecutable(ctx, path, request.ExecutorDigest)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrAgentAdmission, err)
	}
	digest, info, err := executableIdentity(ctx, bin)
	if err != nil || digest != request.ExecutorDigest {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("%w: staged executor identity changed: %v", ErrAgentAdmission, err)
	}
	cfg.executableDir = dir
	cfg.executionGate = executionGate(request)
	if cfg.sandbox != nil {
		cfg.sandbox.WithReadPaths(sandboxPathAliases(dir)...)
	}
	conn, err := spawnAgent(ctx, bin, request.Identity, "artifact", digest, info, nil, cfg)
	if err != nil {
		return nil, err
	}
	admitCtx, cancel := context.WithTimeout(ctx, cfg.dialTimeout)
	defer cancel()
	if err := admitArtifact(admitCtx, conn, request); err != nil {
		conn.Close()
		return nil, fmt.Errorf("%w: %w", ErrAgentAdmission, err)
	}
	return conn, nil
}

// Copy before hashing/spawning: acquisition may atomically replace its path while
// another instance is starting. Only the private, verified snapshot is executed.
func stageExecutable(ctx context.Context, path, expected string) (bin, dir string, err error) {
	source, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", "", err
	}
	defer source.Close()
	info, err := source.Stat()
	if err != nil {
		return "", "", err
	}
	if !info.Mode().IsRegular() {
		return "", "", fmt.Errorf("acquired executor must be a regular file")
	}
	dir, err = os.MkdirTemp("", "codefly-executor-")
	if err != nil {
		return "", "", err
	}
	defer func() {
		if err != nil {
			_ = os.RemoveAll(dir)
		}
	}()
	bin = filepath.Join(dir, "executor")
	output, err := os.OpenFile(bin, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return "", dir, err
	}
	defer output.Close()
	hash := sha256.New()
	buffer := make([]byte, 128*1024)
	for {
		if err = ctx.Err(); err != nil {
			return "", dir, err
		}
		var n int
		n, err = source.Read(buffer)
		if n > 0 {
			if _, writeErr := output.Write(buffer[:n]); writeErr != nil {
				return "", dir, writeErr
			}
			_, _ = hash.Write(buffer[:n])
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", dir, err
		}
	}
	if fmt.Sprintf("sha256:%x", hash.Sum(nil)) != expected {
		return "", dir, fmt.Errorf("acquired executor digest does not match selected executable")
	}
	if err = output.Chmod(0500); err != nil {
		return "", dir, err
	}
	if err = output.Close(); err != nil {
		return "", dir, err
	}
	return bin, dir, nil
}

func admitArtifact(ctx context.Context, conn *AgentConn, request *basev0.ArtifactExecution) error {
	info, err := agentv0.NewAgentClient(conn.GRPCConn()).GetAgentInformation(ctx, &agentv0.AgentInformationRequest{})
	if err != nil {
		return err
	}
	if err := contract.Check(info.GetContract()); err != nil {
		return err
	}
	var advertised []string
	if request.Protocol == artifactexecution.SolutionRender {
		info, err := solution.NewClient(conn.GRPCConn()).GetSolutionInformation(ctx, solution.CeilingInspect(), &solutionv0.GetSolutionInformationRequest{Artifact: &solutionv0.SolutionArtifact{ArtifactDigest: conn.ArtifactDigest()}})
		if err != nil {
			return err
		}
		if info.GetArtifact().GetArtifactDigest() != conn.ArtifactDigest() || !info.GetCapabilities().GetSupportsRender() {
			return fmt.Errorf("solution executor did not acknowledge selected executable and render support")
		}
		advertised = info.GetCapabilities().GetExecutionContracts()
	} else {
		info, err := builderv0.NewBuilderClient(conn.GRPCConn()).BuildCapabilities(ctx, &builderv0.BuildCapabilitiesRequest{})
		if err != nil {
			return err
		}
		advertised = info.GetExecutionContracts()
	}
	return artifactexecution.Check(request, request.Protocol, conn.ArtifactDigest(), advertised)
}

func executionGate(selected *basev0.ArtifactExecution) grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoke grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		var execution *basev0.ArtifactExecution
		var protocol string
		switch method {
		case agentv0.Agent_GetAgentInformation_FullMethodName, builderv0.Builder_BuildCapabilities_FullMethodName, solutionv0.Solution_GetSolutionInformation_FullMethodName, "/grpc.health.v1.Health/Check":
			return invoke(ctx, method, req, reply, cc, opts...)
		case builderv0.Builder_Build_FullMethodName:
			if request, ok := req.(*builderv0.BuildRequest); ok {
				execution = request.Execution
			}
			protocol = artifactexecution.BuilderBuild
		case builderv0.Builder_Deploy_FullMethodName:
			if request, ok := req.(*builderv0.DeploymentRequest); ok {
				execution = request.Execution
			}
			protocol = artifactexecution.BuilderRender
		case solutionv0.Solution_Render_FullMethodName:
			if request, ok := req.(*solutionv0.RenderRequest); ok {
				execution = request.Execution
			}
			protocol = artifactexecution.SolutionRender
		}
		if protocol != selected.Protocol || !proto.Equal(execution, selected) {
			return fmt.Errorf("%w: RPC is not bound to the loaded artifact execution", ErrAgentAdmission)
		}
		return invoke(ctx, method, req, reply, cc, opts...)
	}
}
