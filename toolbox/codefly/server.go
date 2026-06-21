// Package codefly implements the toolbox contract for inspecting a codefly
// workspace — modules, services, and layout — as typed RPCs. Pure Go against
// core/resources (the workspace model), the way the git toolbox uses go-git: no
// shelling out to the codefly CLI.
package codefly

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
	"github.com/codefly-dev/core/resources"
	"github.com/codefly-dev/core/toolbox/registry"
	"github.com/codefly-dev/core/toolbox/respond"
)

// Server inspects the codefly workspace rooted at a directory.
type Server struct {
	*registry.Base

	workspace string
	version   string
}

// New returns a Server bound to a workspace directory (the workspace may be at
// or above it).
func New(workspace, version string) *Server {
	s := &Server{workspace: workspace, version: version}
	s.Base = registry.NewBase(s)
	return s
}

func (s *Server) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return &toolboxv0.IdentityResponse{
		Name:           "codefly",
		Version:        s.version,
		Description:    "Inspect the codefly workspace: modules, services, layout. Read-only.",
		CanonicalFor:   []string{"codefly"},
		SandboxSummary: fmt.Sprintf("reads the codefly workspace at %s; no network", s.workspace),
	}, nil
}

func (s *Server) Tools() []*registry.ToolDefinition {
	return []*registry.ToolDefinition{
		{
			Name:               "codefly.list",
			SummaryDescription: "List the workspace's modules and services. Read-only.",
			LongDescription: "Loads the codefly workspace and returns its `layout`, `modules` (names), and " +
				"`services` (each as module + name). Use to discover what's in the project before acting on a service.",
			InputSchema: respond.Schema(map[string]any{"type": "object", "properties": map[string]any{}}),
			Tags:        []string{"codefly", "read-only", "filesystem"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns `workspace: ...` when the directory isn't inside a codefly workspace.",
			Examples:    []*toolboxv0.ToolExample{{Description: "List everything in the workspace.", Arguments: mustStruct(map[string]any{}), ExpectedOutcome: "{ workspace, layout, modules: [...], services: [{module,name}] }"}},
			Handler:     s.list,
		},
		{
			Name:               "codefly.info",
			SummaryDescription: "Workspace metadata (name, layout, counts). Read-only.",
			LongDescription:    "Returns the workspace `name`, `description`, `layout`, and module/service counts — a quick orientation summary.",
			InputSchema:        respond.Schema(map[string]any{"type": "object", "properties": map[string]any{}}),
			Tags:               []string{"codefly", "read-only", "filesystem"},
			Idempotency:        "idempotent",
			ErrorModes:         "Returns `workspace: ...` when no workspace is found.",
			Examples:           []*toolboxv0.ToolExample{{Description: "Summarize the workspace.", Arguments: mustStruct(map[string]any{}), ExpectedOutcome: "{ name, description, layout, module_count, service_count }"}},
			Handler:            s.info,
		},
	}
}

func (s *Server) list(ctx context.Context, _ *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	ws, err := s.loadWorkspace(ctx)
	if err != nil {
		return respond.Error("workspace: %v", err)
	}
	svcs, err := ws.LoadServiceWithModules(ctx)
	if err != nil {
		return respond.Error("load services: %v", err)
	}
	services := make([]any, 0, len(svcs))
	for _, sm := range svcs {
		services = append(services, map[string]any{"module": sm.Module, "name": sm.Name})
	}
	return respond.Struct(map[string]any{
		"workspace": ws.Name,
		"layout":    ws.Layout,
		"modules":   toAnySlice(ws.ModulesNames()),
		"services":  services,
	})
}

func (s *Server) info(ctx context.Context, _ *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
	ws, err := s.loadWorkspace(ctx)
	if err != nil {
		return respond.Error("workspace: %v", err)
	}
	svcs, _ := ws.LoadServiceWithModules(ctx)
	return respond.Struct(map[string]any{
		"name":          ws.Name,
		"description":   ws.Description,
		"layout":        ws.Layout,
		"module_count":  len(ws.ModulesNames()),
		"service_count": len(svcs),
	})
}

// loadWorkspace loads from the toolbox's directory, falling back to a search up
// the tree (so a service-subdir still resolves the workspace).
func (s *Server) loadWorkspace(ctx context.Context) (*resources.Workspace, error) {
	if ws, err := resources.LoadWorkspaceFromDir(ctx, s.workspace); err == nil && ws != nil {
		return ws, nil
	}
	ws, err := resources.FindWorkspaceUp(ctx)
	if err != nil {
		return nil, err
	}
	if ws == nil {
		return nil, fmt.Errorf("no codefly workspace found at or above %s", s.workspace)
	}
	return ws, nil
}

func toAnySlice(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

func mustStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("codefly toolbox: cannot encode example args: %v", err))
	}
	return s
}
