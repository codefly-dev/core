# Writing a Codefly toolbox plugin

A toolbox plugin is a small Go server with three pieces:

1. one `registry.Descriptor` for identity and sandbox metadata;
2. one slice of `registry.ToolDefinition` values;
3. handler methods for those tools.

Core owns the gRPC server, identity RPC, catalog projections, schema validation,
dispatch, response envelopes, version environment, and process startup.

## Minimal server

```go
package example

import (
    "context"

    toolboxv0 "github.com/codefly-dev/core/generated/go/codefly/services/toolbox/v0"
    "github.com/codefly-dev/core/toolbox/registry"
    "github.com/codefly-dev/core/toolbox/respond"
)

type Server struct {
    *registry.Base
}

func New(version string) *Server {
    server := &Server{}
    server.Base = registry.NewBase(registry.Descriptor{
        Name:           "example",
        Version:        version,
        Description:    "Example read-only tools.",
        CanonicalFor:   []string{"examplectl"},
        SandboxSummary: "reads workspace; network denied",
    }, server.Tools()...)
    return server
}

func (s *Server) Tools() []*registry.ToolDefinition {
    return []*registry.ToolDefinition{
        {
            Name:               "example.echo",
            SummaryDescription: "Return the supplied text. Read-only.",
            LongDescription:    "Returns text unchanged; useful for connectivity checks.",
            InputSchema: respond.Schema(map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "text": map[string]any{"type": "string"},
                },
                "required": []any{"text"},
            }),
            Tags:        []string{"read-only"},
            Idempotency: "idempotent",
            ErrorModes:  "Schema validation rejects missing or non-string text.",
            Examples: []*toolboxv0.ToolExample{{
                Description:     "Echo a greeting.",
                Arguments:       respond.MustStruct(map[string]any{"text": "hello"}),
                ExpectedOutcome: "{ text: 'hello' }",
            }},
            Handler: s.echo,
        },
    }
}

func (s *Server) echo(_ context.Context, request *toolboxv0.CallToolRequest) *toolboxv0.CallToolResponse {
    text, _ := respond.Args(request)["text"].(string)
    return respond.Struct(map[string]any{"text": text})
}
```

`registry.NewBase` rejects missing descriptor names, nil definitions, empty tool
names, and duplicate tool names at startup. It adds the descriptor name to every
tool's tags, so definitions only declare capability tags such as `read-only`,
`network`, `filesystem`, or `destructive`.

The input schema is enforced before the handler runs. Handlers therefore focus
on domain behavior; use `respond.Args`, `respond.Error`, `respond.Struct`, and
`respond.Text` for the remaining request and response shaping.

## Minimal executable

```go
package main

import (
    "github.com/codefly-dev/core/agents"
    coretoolbox "github.com/codefly-dev/core/toolbox"
    example "github.com/example/toolbox-example"
)

func main() {
    agents.ServeToolbox(example.New(coretoolbox.Version()))
}
```

Use `coretoolbox.Environment` for optional scalar configuration and
`coretoolbox.EnvironmentList` for comma-separated lists. The host supplies
`CODEFLY_TOOLBOX_VERSION`; direct development launches receive `0.0.0-dev`.

## Dynamic protocol adapters

Native plugins should declare their final identity and tools in `NewBase`.
Adapters that discover a remote surface during a handshake can atomically call
`Base.SetDescriptor` and `Base.SetTools` after discovery. `toolbox/mcprev` is the
reference implementation.

## Release checks

Run both workspace and standalone checks so a local `go.work` cannot hide an
unpublished core dependency:

```sh
go test ./...
go test -race ./...
go vet ./...
GOWORK=off go test ./...
GOWORK=off go vet ./...
```
