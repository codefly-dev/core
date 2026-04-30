package docker

import (
	"context"
	"fmt"
	"sync"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"google.golang.org/protobuf/types/known/structpb"

	toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
)

// Server implements codefly.services.toolbox.v0.Toolbox for Docker
// inspection ops.
//
// Constructed lazily — the Docker daemon connection isn't established
// until the first tool call. This means a Server is cheap to create
// and tests don't need a live daemon to exercise schema/dispatch
// logic; only the daemon-touching tools need it.
//
// The lazy init is once-only and safe under concurrent CallTool —
// sync.Once guards the client creation so concurrent first callers
// don't both create + leak Docker clients (a real bug caught in
// review of an earlier draft that did read-then-write without a lock).
type Server struct {
	toolboxv0.UnimplementedToolboxServer

	version string

	// initOnce guards the one-shot Docker client creation. After it
	// fires, cli + initErr are stable for the Server's lifetime.
	initOnce sync.Once
	cli      *client.Client
	initErr  error
}

// New returns a Server.
func New(version string) *Server {
	return &Server{version: version}
}

// Close releases the Docker SDK client. Idempotent.
//
// Calling Close before any CallTool is a no-op (initOnce hasn't
// fired). Calling it after CallTool but while another goroutine is
// mid-CallTool risks closing the client out from under it — by
// convention Close should be called only when the Server is being
// torn down and no further calls are in flight.
func (s *Server) Close() error {
	if s.cli == nil {
		return nil
	}
	err := s.cli.Close()
	s.cli = nil
	return err
}

// dockerClient returns the lazily-initialized Docker SDK client.
// Concurrent first callers all observe the same client (or the
// same init error) — sync.Once enforces at-most-once creation.
func (s *Server) dockerClient() (*client.Client, error) {
	s.initOnce.Do(func() {
		cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
		if err != nil {
			s.initErr = fmt.Errorf("docker client init: %w", err)
			return
		}
		s.cli = cli
	})
	if s.initErr != nil {
		return nil, s.initErr
	}
	return s.cli, nil
}

// --- Identity ----------------------------------------------------

func (s *Server) Identity(_ context.Context, _ *toolboxv0.IdentityRequest) (*toolboxv0.IdentityResponse, error) {
	return &toolboxv0.IdentityResponse{
		Name:        "docker",
		Version:     s.version,
		Description: "Docker image and container inspection. Canonical owner of the `docker` binary.",
		CanonicalFor: []string{"docker"},
		SandboxSummary: "needs unix socket /var/run/docker.sock; reads + writes deny by default",
	}, nil
}

// --- Tools -------------------------------------------------------

func (s *Server) ListTools(_ context.Context, _ *toolboxv0.ListToolsRequest) (*toolboxv0.ListToolsResponse, error) {
	return &toolboxv0.ListToolsResponse{
		Tools: []*toolboxv0.Tool{
			{
				Name:        "docker.list_containers",
				Description: "List containers known to the local daemon (running by default; pass all=true for stopped too).",
				InputSchema: mustSchema(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"all": map[string]any{
							"type":        "boolean",
							"description": "Include stopped containers. Default false.",
						},
					},
				}),
				Destructive: false,
			},
			{
				Name:        "docker.list_images",
				Description: "List images present in the local daemon.",
				InputSchema: mustSchema(map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				}),
				Destructive: false,
			},
			{
				Name:        "docker.inspect_container",
				Description: "Inspect a container by ID or name; returns full JSON (state, mounts, networking, env).",
				InputSchema: mustSchema(map[string]any{
					"type": "object",
					"properties": map[string]any{
						"id": map[string]any{
							"type":        "string",
							"description": "Container ID or name.",
						},
					},
					"required": []any{"id"},
				}),
				Destructive: false,
			},
		},
	}, nil
}

func (s *Server) CallTool(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	switch req.Name {
	case "docker.list_containers":
		return s.listContainers(ctx, req)
	case "docker.list_images":
		return s.listImages(ctx, req)
	case "docker.inspect_container":
		return s.inspectContainer(ctx, req)
	default:
		return errResp("unknown tool %q (call ListTools to enumerate)", req.Name), nil
	}
}

// --- Tool implementations ----------------------------------------

func (s *Server) listContainers(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	cli, err := s.dockerClient()
	if err != nil {
		return errResp("%v", err), nil
	}

	all := false
	if v, ok := argMap(req)["all"].(bool); ok {
		all = v
	}

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: all})
	if err != nil {
		return errResp("docker list: %v", err), nil
	}
	out := make([]any, 0, len(containers))
	for _, c := range containers {
		out = append(out, map[string]any{
			"id":     c.ID[:12],
			"image":  c.Image,
			"status": c.Status,
			"state":  c.State,
			"names":  toAnySlice(c.Names),
		})
	}
	return structResp(map[string]any{"containers": out}), nil
}

func (s *Server) listImages(ctx context.Context, _ *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	cli, err := s.dockerClient()
	if err != nil {
		return errResp("%v", err), nil
	}
	images, err := cli.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return errResp("docker image list: %v", err), nil
	}
	out := make([]any, 0, len(images))
	for _, im := range images {
		out = append(out, map[string]any{
			"id":           im.ID,
			"repo_tags":    toAnySlice(im.RepoTags),
			"size":         im.Size,
			"created_unix": im.Created,
		})
	}
	return structResp(map[string]any{"images": out}), nil
}

func (s *Server) inspectContainer(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	id, ok := argMap(req)["id"].(string)
	if !ok || id == "" {
		return errResp("docker.inspect_container: id is required"), nil
	}
	cli, err := s.dockerClient()
	if err != nil {
		return errResp("%v", err), nil
	}
	info, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		return errResp("inspect: %v", err), nil
	}
	// Surface a curated subset so the agent gets useful structure
	// without the full 50-field SDK type. Callers who need the full
	// dump can ask for it later via a `verbose: true` flag.
	out := map[string]any{
		"id":      info.ID,
		"name":    info.Name,
		"image":   info.Image,
		"created": info.Created,
		"running": info.State != nil && info.State.Running,
	}
	if info.State != nil {
		out["status"] = info.State.Status
		out["exit_code"] = info.State.ExitCode
	}
	return structResp(out), nil
}

// --- Helpers (mirror toolbox/git for consistency) ----------------

func argMap(req *toolboxv0.CallToolRequest) map[string]any {
	if req.Arguments == nil {
		return map[string]any{}
	}
	return req.Arguments.AsMap()
}

func errResp(format string, args ...any) *toolboxv0.CallToolResponse {
	return &toolboxv0.CallToolResponse{Error: fmt.Sprintf(format, args...)}
}

func structResp(payload map[string]any) *toolboxv0.CallToolResponse {
	s, err := structpb.NewStruct(payload)
	if err != nil {
		return errResp("internal: cannot marshal response: %v", err)
	}
	return &toolboxv0.CallToolResponse{
		Content: []*toolboxv0.Content{
			{Body: &toolboxv0.Content_Structured{Structured: s}},
		},
	}
}

func mustSchema(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("bad input schema: %v", err))
	}
	return s
}

// toAnySlice converts []string → []any for protobuf Struct compat.
// Struct's value type is map[string]any with leaves of any/string/
// number/bool/nil/repeated; []string isn't directly assignable.
func toAnySlice(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
