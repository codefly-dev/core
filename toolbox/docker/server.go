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
	"github.com/codefly-dev/core/toolbox/internal/registry"
	"github.com/codefly-dev/core/toolbox/internal/respond"
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

// tools is the source of truth — all three RPC shapes project from
// here. See git/server.go for convention notes.
func (s *Server) tools() []*registry.ToolDefinition {
	return []*registry.ToolDefinition{
		{
			Name:               "docker.list_containers",
			SummaryDescription: "List containers from the local daemon. Pass all=true to include stopped. Read-only.",
			LongDescription: "Returns the running containers from the local Docker daemon as a structured " +
				"list. Each entry has id (12-char prefix), image, status, state, and names. Pass all=true " +
				"to include stopped containers — useful when investigating recent crashes.",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"all": map[string]any{
						"type":        "boolean",
						"description": "Include stopped containers. Default false.",
					},
				},
			}),
			Tags:        []string{"docker", "read-only"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns 'docker list: ...' when the daemon is unreachable, or 'docker client init: ...' on connection setup failure.",
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     "List currently-running containers.",
					Arguments:       mustDockerStruct(map[string]any{}),
					ExpectedOutcome: "{ containers: [...] } — empty array if nothing's running.",
				},
				{
					Description:     "Include stopped containers (post-mortem investigation).",
					Arguments:       mustDockerStruct(map[string]any{"all": true}),
					ExpectedOutcome: "Same shape, but includes containers in 'exited' state too.",
				},
			},
		},
		{
			Name:               "docker.list_images",
			SummaryDescription: "List images present in the local Docker daemon. Read-only.",
			LongDescription:    "Returns every image cached in the local daemon — id, repo_tags, size, creation timestamp.",
			InputSchema: respond.Schema(map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			}),
			Tags:        []string{"docker", "read-only"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns 'docker image list: ...' when the daemon is unreachable.",
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     "Inventory the local image cache.",
					Arguments:       mustDockerStruct(map[string]any{}),
					ExpectedOutcome: "{ images: [{ id, repo_tags, size, created_unix }, ...] }",
				},
			},
		},
		{
			Name:               "docker.inspect_container",
			SummaryDescription: "Inspect a container by ID or name; returns curated state + metadata. Read-only.",
			LongDescription: "Returns a curated subset of `docker inspect` output: id, name, image, created, " +
				"running, status, exit_code. The full SDK type has 50+ fields; we surface the diagnostically " +
				"interesting ones to keep responses compact. A future version may add `verbose=true` to dump everything.",
			InputSchema: respond.Schema(map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{
						"type":        "string",
						"description": "Container ID or name.",
					},
				},
				"required": []any{"id"},
			}),
			Tags:        []string{"docker", "read-only"},
			Idempotency: "idempotent",
			ErrorModes:  "Returns 'docker.inspect_container: id is required' when id missing, 'inspect: ...' when the container doesn't exist or daemon is unreachable.",
			Examples: []*toolboxv0.ToolExample{
				{
					Description:     "Inspect a container by short ID.",
					Arguments:       mustDockerStruct(map[string]any{"id": "abc123"}),
					ExpectedOutcome: "{ id, name, image, running, status, exit_code }.",
				},
			},
		},
	}
}

func (s *Server) ListTools(_ context.Context, _ *toolboxv0.ListToolsRequest) (*toolboxv0.ListToolsResponse, error) {
	return &toolboxv0.ListToolsResponse{Tools: registry.AsTools(s.tools())}, nil
}

func (s *Server) ListToolSummaries(_ context.Context, req *toolboxv0.ListToolSummariesRequest) (*toolboxv0.ListToolSummariesResponse, error) {
	return &toolboxv0.ListToolSummariesResponse{Tools: registry.AsSummaries(s.tools(), req.GetTagsFilter())}, nil
}

func (s *Server) DescribeTool(_ context.Context, req *toolboxv0.DescribeToolRequest) (*toolboxv0.DescribeToolResponse, error) {
	spec := registry.FindSpec(s.tools(), req.GetName())
	if spec == nil {
		return &toolboxv0.DescribeToolResponse{
			Error: fmt.Sprintf("unknown tool %q (call ListToolSummaries to enumerate)", req.GetName()),
		}, nil
	}
	return &toolboxv0.DescribeToolResponse{Tool: spec}, nil
}

func mustDockerStruct(m map[string]any) *structpb.Struct {
	s, err := structpb.NewStruct(m)
	if err != nil {
		panic(fmt.Sprintf("docker toolbox: cannot encode example args: %v", err))
	}
	return s
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
		return respond.Error("unknown tool %q (call ListTools to enumerate)", req.Name), nil
	}
}

// --- Tool implementations ----------------------------------------

func (s *Server) listContainers(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	cli, err := s.dockerClient()
	if err != nil {
		return respond.Error("%v", err), nil
	}

	all := false
	if v, ok := respond.Args(req)["all"].(bool); ok {
		all = v
	}

	containers, err := cli.ContainerList(ctx, container.ListOptions{All: all})
	if err != nil {
		return respond.Error("docker list: %v", err), nil
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
	return respond.Struct(map[string]any{"containers": out}), nil
}

func (s *Server) listImages(ctx context.Context, _ *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	cli, err := s.dockerClient()
	if err != nil {
		return respond.Error("%v", err), nil
	}
	images, err := cli.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return respond.Error("docker image list: %v", err), nil
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
	return respond.Struct(map[string]any{"images": out}), nil
}

func (s *Server) inspectContainer(ctx context.Context, req *toolboxv0.CallToolRequest) (*toolboxv0.CallToolResponse, error) {
	id, ok := respond.Args(req)["id"].(string)
	if !ok || id == "" {
		return respond.Error("docker.inspect_container: id is required"), nil
	}
	cli, err := s.dockerClient()
	if err != nil {
		return respond.Error("%v", err), nil
	}
	info, err := cli.ContainerInspect(ctx, id)
	if err != nil {
		return respond.Error("inspect: %v", err), nil
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
	return respond.Struct(out), nil
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
