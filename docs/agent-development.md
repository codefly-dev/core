# Writing a Codefly Service Plugin

This document covers service plugins. For callable capability plugins, see
[`toolbox-development.md`](toolbox-development.md).

A service plugin is a small gRPC process that tells Codefly how to create, run,
test, build, and deploy one kind of service. Core owns the protocol, lifecycle
responses, configuration plumbing, template rendering, and common deployment
pipeline. A plugin should contain only the behavior unique to its technology.

## Pick the plugin shape first

Use one of these shapes instead of copying the nearest plugin blindly:

| Shape | Runs | Typical deploy behavior |
| --- | --- | --- |
| Application | User code, such as Go, Rust, FastAPI, or Next.js | Built image plus own/dependency endpoints and configuration |
| Managed resource | A database or infrastructure service, such as Redis | Stock workload plus an exported connection configuration |
| External or migration-only resource | No managed server workload | Optional migration Job only; never pretend to deploy the server |
| Gateway | Generated proxy/router configuration | Application workload plus a route-generation hook |

Managed and external resources are deliberately different. Turning migrations
off for a managed database must omit only its migration Job, not its StatefulSet
and Service.

## Recommended layout

```text
service-example/
├── agent.codefly.yaml
├── go.mod
├── main.go                 # agent metadata, Service, registration
├── builder.go              # create/build/deploy specializations
├── runtime.go              # local run/test lifecycle
├── deployment_test.go      # one-line manifest conformance test
└── templates/
    ├── agent/
    ├── factory/
    └── deployment/kustomize/
        ├── base/
        └── overlays/environment/
```

`agent.codefly.yaml` is the published plugin identity:

```yaml
publisher: example.com
kind: codefly:service
name: example
version: 0.0.1
```

## Base service and registration

Embed `*services.Base`; do not embed generated protobuf servers or manage the
gRPC listener yourself.

```go
type Settings struct {
    HotReload bool `yaml:"hot-reload"`
}

type Service struct {
    *services.Base
    *Settings
    HTTPEndpoint *basev0.Endpoint
}

func NewService() *Service {
    return &Service{
        Base:     services.NewServiceBase(context.Background(), agent.Of(resources.ServiceAgent)),
        Settings: &Settings{},
    }
}

func main() {
    service := NewService()
    agents.Serve(agents.PluginRegistration{
        Agent:   service,
        Runtime: NewRuntime(),
        Builder: NewBuilder(),
    })
}
```

Each registration gets its own `Service` instance because runtime and builder
state have different lifetimes.

## Builder: implement only the special cases

Embed `*services.DefaultBuilder`. It provides correct successful no-op `Init`,
`Update`, `Sync`, and `Build` responses plus an empty `Communicate` stream.
Implement a method only when the plugin has real work for that phase.

```go
type Builder struct {
    *services.DefaultBuilder
    *Service
}

func NewBuilder() *Builder {
    service := NewService()
    return &Builder{
        DefaultBuilder: services.NewDefaultBuilder(service.Builder),
        Service:        service,
    }
}
```

The conventional `Load` path is declarative too:

```go
func (s *Builder) Load(ctx context.Context, req *builderv0.LoadRequest) (*builderv0.LoadResponse, error) {
    return s.Builder.LoadService(ctx, req, services.BuilderLoad{
        Settings:         s.Settings,
        Requirements:     requirements,
        FactoryTemplates: factoryFS,
        ResolveEndpoints: func(ctx context.Context, endpoints []*basev0.Endpoint) error {
            endpoint, err := resources.FindHTTPEndpoint(ctx, endpoints)
            if err != nil {
                return err
            }
            s.HTTPEndpoint = endpoint
            return nil
        },
    })
}
```

Core now owns identity/settings loading, dependency localization, creation-mode
Getting Started rendering, endpoint loading, catch configuration, and the
structured response.

## Deployment golden paths

### Application

The common application path is a single call:

```go
func (s *Builder) Deploy(ctx context.Context, req *builderv0.DeploymentRequest) (*builderv0.DeploymentResponse, error) {
    return s.Builder.DeployKustomize(ctx, req, services.KustomizeDeployment{
        EnvironmentVariables: s.EnvironmentVariables,
        Templates:             deploymentFS,
        Inputs:                services.ApplicationDeploymentInputs(),
    })
}
```

This collects container-local own endpoints, dependency endpoints, service and
dependency configuration, separates secrets, renders Kustomize, and returns a
structured response.

If an application intentionally consumes fewer inputs, declare them explicitly:

```go
Inputs: services.DeploymentInputs{
    OwnConfiguration:         true,
    DependencyConfigurations: true,
},
```

### Managed resource

A managed resource usually has one unique operation: derive the connection
configuration clients should receive.

```go
func (s *Builder) Deploy(ctx context.Context, req *builderv0.DeploymentRequest) (*builderv0.DeploymentResponse, error) {
    return s.Builder.DeployKustomize(ctx, req, services.KustomizeDeployment{
        EnvironmentVariables: s.EnvironmentVariables,
        Templates:             deploymentFS,
        Prepare: func(ctx context.Context, deployment *services.KustomizeDeploymentContext) error {
            instance, err := resources.FindNetworkInstanceInNetworkMappings(
                ctx, req.GetNetworkMappings(), s.TCPEndpoint, resources.NewPublicNetworkAccess(),
            )
            if err != nil {
                return err
            }
            configuration, err := s.CreateConnectionConfiguration(ctx, req.GetConfiguration(), instance)
            if err != nil {
                return err
            }
            return deployment.ExportConfiguration(ctx, configuration)
        },
    })
}
```

The preparation context can also set `deployment.Parameters`, call
`deployment.AddConfigMap(...)`, or call `deployment.AddSecrets(...)`. Raw secret
values added this way are base64-encoded by core before rendering.

### Gateway

Gateways use the same pipeline and set generated configuration in the hook:

```go
Prepare: func(ctx context.Context, deployment *services.KustomizeDeploymentContext) error {
    config, err := buildRoutes(ctx, req.GetDependenciesNetworkMappings())
    if err != nil {
        return err
    }
    deployment.Parameters = Parameters{Configuration: string(config)}
    return nil
},
```

### Migration-only resource

Keep the early no-migration return only when the plugin truly owns no server
workload. A managed database should always render its workload and conditionally
include its Job in `kustomization.yaml.tmpl`:

```gotemplate
resources:
  - stateful-set.yaml
  - service.yaml
{{- if .Deployment.Parameters.WithMigration }}
  - job.yaml
{{- end }}
```

## Deployment template context

Core exposes stable shallow fields for common templates:

| Field | Meaning |
| --- | --- |
| `.Name` | DNS-safe service name |
| `.Namespace` | Kubernetes namespace |
| `.Environment` | target environment |
| `.Image` | full tag- or digest-pinned image reference |
| `.Sha` | deployment change marker |
| `.Replicas` | default replica count |
| `.ConfigMap` | non-secret environment map |
| `.SecretMap` | base64-encoded secret map |
| `.Deployment.Parameters` | plugin-specific parameter object |

Render maps explicitly; inserting a Go map directly is not valid Kubernetes
YAML:

```gotemplate
data:
{{- range $key, $value := .SecretMap }}
  {{ $key }}: "{{ $value }}"
{{- end }}
```

## Runtime

Embed `*services.DefaultRuntime`; it supplies the shared `Information` method.
Start, Stop, Destroy, and Test intentionally remain explicit because process
ownership and cleanup cannot safely be guessed.

```go
type Runtime struct {
    *services.DefaultRuntime
    *Service
    runner *exec.Cmd
}

func NewRuntime() *Runtime {
    service := NewService()
    return &Runtime{
        DefaultRuntime: services.NewDefaultRuntime(service.Runtime),
        Service:        service,
    }
}
```

Use `Runtime.LoadService` for the standard load path, response helpers such as
`InitResponse`, `StartError`, `TestResponseWithResults`, and `DestroyResponse`,
and `Base.StopWatcher()` for watcher cleanup. When a spawned process exits after
Start, call `Runtime.MarkRunnerExited(err)` so the CLI observes the failure.

## Response states: use helpers, not aliases

Do not introduce local aliases for generated values such as
`runtimev0.InitStatus_READY`. The generated enum is the wire contract; another
alias adds a second vocabulary that can drift. Plugin code normally should not
mention the enum at all—return `s.Runtime.InitResponse()` or an error helper.
Core owns the exact generated status value.

Error helpers return a structured RPC response and a nil transport error. This
lets the CLI display the lifecycle failure instead of losing it as a generic
gRPC error.

## Required deployment test

Every plugin with deployment templates should have this test:

```go
func TestDeploymentTemplates(t *testing.T) {
    agenttesting.AssertKustomizeTemplates(t, deploymentFS, Parameters{})
}
```

It renders representative config, secrets, image data, and plugin parameters;
then checks for unexpanded expressions, malformed YAML, invalid Secret/ConfigMap
data, and dangling Kustomize resources. It needs no cluster or external binary.

Add focused assertions for conditional resources such as migration Jobs.

## Release checks

Workspace tests prove integration against local core. Standalone tests prove the
plugin's published core pin is sufficient. Run both:

```sh
go test ./...
go test -race ./...
go vet ./...
GOWORK=off go test ./...
GOWORK=off go vet ./...
```

Use `codefly agent deps` while developing locally and
`codefly agent deps --pin <core-version> --ci` before publishing.

The best current small reference is `service-redis`; `service-envoy` shows the
gateway hook, and `service-postgres` shows a managed resource with an optional
migration Job.
