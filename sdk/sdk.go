// Package sdk provides helpers for codefly-managed services.
//
// For plugin/agent code, use core/cli.WithDependencies directly.
// This package is for user-facing service code.
package sdk

import (
	"context"
	"fmt"
	"os"
	"path"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/codefly-dev/core/agents/manager"
	basev0 "github.com/codefly-dev/core/generated/go/codefly/base/v0"
	builderv0 "github.com/codefly-dev/core/generated/go/codefly/services/builder/v0"
	runtimev0 "github.com/codefly-dev/core/generated/go/codefly/services/runtime/v0"
	"github.com/codefly-dev/core/network"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/shared"
)

// Env manages a set of codefly services.
type Env struct {
	mu      sync.Mutex
	agents  []string
	running []*runningService
	tmpDir  string
	configs map[string]map[string]string // agent -> name -> connection string
}

type runningService struct {
	name    string
	conn    *manager.AgentConn
	runtime runtimev0.RuntimeClient
}

// New creates a new environment.
//
// Deprecated: For plugin tests, use cli.WithDependencies instead.
func New() *Env {
	return &Env{
		configs: make(map[string]map[string]string),
	}
}

// Add registers a codefly service agent to be started.
//
// Deprecated: Use cli.WithDependencies which reads service.codefly.yaml.
func (e *Env) Add(agentName string) *Env {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.agents = append(e.agents, agentName)
	return e
}

// Load reads service.codefly.yaml from dir and adds all declared
// service-dependencies.
func (e *Env) Load(dir string) (*Env, error) {
	data, err := os.ReadFile(path.Join(dir, "service.codefly.yaml"))
	if err != nil {
		return e, fmt.Errorf("read service.codefly.yaml: %w", err)
	}
	var svc serviceYAML
	if err := yaml.Unmarshal(data, &svc); err != nil {
		return e, fmt.Errorf("parse service.codefly.yaml: %w", err)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, dep := range svc.ServiceDependencies {
		e.agents = append(e.agents, dep.Name)
	}
	return e, nil
}

type serviceYAML struct {
	ServiceDependencies []struct {
		Name string `yaml:"name"`
	} `yaml:"service-dependencies"`
}

// Connection returns a connection string for a running service.
func (e *Env) Connection(agentName, name string) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if m, ok := e.configs[agentName]; ok {
		return m[name]
	}
	return ""
}

// Connections returns all connection strings for a running service.
func (e *Env) Connections(agentName string) map[string]string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if m, ok := e.configs[agentName]; ok {
		cp := make(map[string]string, len(m))
		for k, v := range m {
			cp[k] = v
		}
		return cp
	}
	return nil
}

// Start starts all registered services.
func (e *Env) Start(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	tmpDir, err := os.MkdirTemp("", "codefly-sdk-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	e.tmpDir = tmpDir

	for _, agentName := range e.agents {
		if err := e.startAgent(ctx, agentName); err != nil {
			e.stopLocked(ctx)
			return fmt.Errorf("start %s: %w", agentName, err)
		}
	}
	return nil
}

// Stop destroys all running services and cleans up.
func (e *Env) Stop(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.stopLocked(ctx)
	return nil
}

func (e *Env) stopLocked(ctx context.Context) {
	for _, rs := range e.running {
		if rs.runtime != nil {
			_, _ = rs.runtime.Destroy(ctx, &runtimev0.DestroyRequest{})
		}
		if rs.conn != nil {
			rs.conn.Close()
		}
	}
	e.running = nil
	if e.tmpDir != "" {
		os.RemoveAll(e.tmpDir)
		e.tmpDir = ""
	}
}

func (e *Env) startAgent(ctx context.Context, agentName string) error {
	agent, err := resources.ParseAgent(ctx, resources.ServiceAgent, agentName+":latest")
	if err != nil {
		return fmt.Errorf("parse agent %s: %w", agentName, err)
	}
	if err := manager.FindLocalLatest(ctx, agent); err != nil {
		return fmt.Errorf("agent %s not installed: %w", agentName, err)
	}

	agentConn, err := manager.Load(ctx, agent)
	if err != nil {
		return fmt.Errorf("load agent %s: %w", agentName, err)
	}

	grpcConn := agentConn.GRPCConn()
	builder := builderv0.NewBuilderClient(grpcConn)
	runtime := runtimev0.NewRuntimeClient(grpcConn)

	workspace := &resources.Workspace{Name: "sdk"}
	serviceName := fmt.Sprintf("%s-%d", agentName, time.Now().UnixMilli())
	service := resources.Service{Name: serviceName, Version: "sdk"}
	if err := service.SaveAtDir(ctx, path.Join(e.tmpDir, "mod", serviceName)); err != nil {
		agentConn.Close()
		return fmt.Errorf("save service: %w", err)
	}
	service.WithModule("mod")

	identity := &basev0.ServiceIdentity{
		Name:                serviceName,
		Module:              "mod",
		Workspace:           workspace.Name,
		WorkspacePath:       e.tmpDir,
		RelativeToWorkspace: fmt.Sprintf("mod/%s", serviceName),
	}

	if _, err := builder.Load(ctx, &builderv0.LoadRequest{
		DisableCatch: true,
		Identity:     identity,
		CreationMode: &builderv0.CreationMode{Communicate: false},
	}); err != nil {
		agentConn.Close()
		return fmt.Errorf("builder.Load: %w", err)
	}
	if _, err := builder.Create(ctx, &builderv0.CreateRequest{}); err != nil {
		agentConn.Close()
		return fmt.Errorf("builder.Create: %w", err)
	}

	env := resources.LocalEnvironment()
	loadResp, err := runtime.Load(ctx, &runtimev0.LoadRequest{
		Identity:     identity,
		Environment:  shared.Must(env.Proto()),
		DisableCatch: true,
	})
	if err != nil {
		agentConn.Close()
		return fmt.Errorf("runtime.Load: %w", err)
	}

	networkManager, err := network.NewRuntimeManager(ctx, nil)
	if err != nil {
		agentConn.Close()
		return fmt.Errorf("network manager: %w", err)
	}
	networkManager.WithTemporaryPorts()

	serviceIdentity := &resources.ServiceIdentity{
		Name:                serviceName,
		Module:              "mod",
		Workspace:           workspace.Name,
		WorkspacePath:       e.tmpDir,
		RelativeToWorkspace: fmt.Sprintf("mod/%s", serviceName),
	}

	networkMappings, err := networkManager.GenerateNetworkMappings(ctx, env, workspace, serviceIdentity, loadResp.Endpoints)
	if err != nil {
		agentConn.Close()
		return fmt.Errorf("network mappings: %w", err)
	}

	initResp, err := runtime.Init(ctx, &runtimev0.InitRequest{
		RuntimeContext:          resources.NewRuntimeContextFree(),
		ProposedNetworkMappings: networkMappings,
	})
	if err != nil {
		agentConn.Close()
		return fmt.Errorf("runtime.Init: %w", err)
	}

	if _, err := runtime.Start(ctx, &runtimev0.StartRequest{}); err != nil {
		agentConn.Close()
		return fmt.Errorf("runtime.Start: %w", err)
	}

	configs, err := resources.ExtractConfiguration(initResp.RuntimeConfigurations, resources.NewRuntimeContextNative())
	if err != nil {
		agentConn.Close()
		return fmt.Errorf("extract config: %w", err)
	}

	connMap := make(map[string]string)
	if configs != nil {
		for _, info := range configs.Infos {
			for _, val := range info.ConfigurationValues {
				if val.Key == "connection" {
					connMap[info.Name] = val.Value
				}
			}
		}
	}
	e.configs[agentName] = connMap

	e.running = append(e.running, &runningService{
		name:    agentName,
		conn:    agentConn,
		runtime: runtime,
	})

	return nil
}
